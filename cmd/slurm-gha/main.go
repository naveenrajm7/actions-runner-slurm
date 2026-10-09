package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/bootstrap"
	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	githubclient "github.com/naveenrajm7/actions-runner-slurm/internal/github"
	"github.com/naveenrajm7/actions-runner-slurm/internal/launch"
	"github.com/naveenrajm7/actions-runner-slurm/internal/observe"
	"github.com/naveenrajm7/actions-runner-slurm/internal/reconcile"
	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		path := flags.String("config", "", "configuration file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" {
			return errors.New("serve requires --config")
		}
		return serve(*path)
	case "validate-config":
		flags := flag.NewFlagSet("validate-config", flag.ContinueOnError)
		path := flags.String("config", "", "configuration file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" {
			return errors.New("validate-config requires --config")
		}
		_, err := config.Load(*path)
		if err == nil {
			fmt.Println("configuration is valid")
		}
		return err
	case "doctor":
		flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
		path := flags.String("config", "", "configuration file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" {
			return errors.New("doctor requires --config")
		}
		return doctor(*path)
	case "status":
		flags := flag.NewFlagSet("status", flag.ContinueOnError)
		path := flags.String("config", "", "configuration file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" {
			return errors.New("status requires --config")
		}
		return status(*path)
	case "generate-key":
		flags := flag.NewFlagSet("generate-key", flag.ContinueOnError)
		output := flags.String("output", "", "new key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *output == "" {
			return errors.New("generate-key requires --output")
		}
		return generateKey(*output)
	case "slurm-smoke":
		return slurmSmoke(args[1:])
	case "version":
		fmt.Printf("slurm-gha %s (%s)\n", version, commit)
		return nil
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: slurm-gha <serve|validate-config|doctor|status|generate-key|slurm-smoke|version> [options]")
}

func serve(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	state, err := store.Open(cfg.Service.StatePath)
	if err != nil {
		return err
	}
	defer state.Close()
	slurmClient, err := newSlurmClient(cfg.Slurm)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := slurmClient.Ping(ctx); err != nil {
		return fmt.Errorf("Slurm readiness check: %w", err)
	}
	manager, err := githubclient.NewManager(cfg.GitHub, logger.WithGroup("github"))
	if err != nil {
		return err
	}
	if err := manager.EnsureScaleSets(ctx, cfg.ScaleSets); err != nil {
		return err
	}
	controller, err := reconcile.New(state, slurmClient, cfg.Service, cfg.ScaleSets, logger.WithGroup("reconcile"))
	if err != nil {
		return err
	}
	if err := controller.ReconcileSlurm(ctx); err != nil {
		return fmt.Errorf("startup reconciliation: %w", err)
	}
	sealer, err := bootstrap.LoadSealer(cfg.Service.JITEncryptionKeyFile)
	if err != nil {
		return err
	}
	claimHandler, err := bootstrap.NewHandler(state, slurmClient, manager, sealer)
	if err != nil {
		return err
	}
	health := &observe.Health{}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.Health)
	mux.HandleFunc("/readyz", health.Ready)
	mux.Handle("/api/v1/leases/", claimHandler)
	server := &http.Server{
		Addr: cfg.Service.ListenAddress, Handler: mux,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 75 * time.Second,
		WriteTimeout: 75 * time.Second, IdleTimeout: 90 * time.Second,
	}
	errCh := make(chan error, 3)
	go func() {
		err := server.ListenAndServeTLS(cfg.Service.TLS.CertificateFile, cfg.Service.TLS.KeyFile)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTPS server: %w", err)
		}
	}()
	health.SetReady(true)
	go func() {
		if err := manager.RunListeners(ctx, controller); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	go periodicReconcile(ctx, controller, cfg.Service.ReconcileInterval, logger, errCh)
	logger.Info("service ready", "listen_address", cfg.Service.ListenAddress, "classes", len(cfg.ScaleSets))
	select {
	case <-ctx.Done():
		err = nil
	case err = <-errCh:
		stop()
	}
	health.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if shutdownErr := server.Shutdown(shutdownCtx); err == nil && shutdownErr != nil {
		err = shutdownErr
	}
	return err
}

func periodicReconcile(ctx context.Context, controller *reconcile.Controller, interval time.Duration, logger *slog.Logger, errCh chan<- error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := controller.ReconcileSlurm(ctx); err != nil {
				logger.Error("periodic Slurm reconciliation failed", "error", err)
			}
		}
	}
}

func doctor(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	client, err := newSlurmClient(cfg.Slurm)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Slurm.RequestTimeout)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("slurmrestd: %w", err)
	}
	checks := map[string]string{"configuration": "ok", "slurmrestd": "ok"}
	for _, class := range cfg.ScaleSets {
		backend, err := launch.BackendForMode(class.Execution.Mode)
		if err != nil {
			return fmt.Errorf("class %s: %w", class.Name, err)
		}
		if err := backend.Ready(); err != nil {
			return fmt.Errorf("class %s: %w", class.Name, err)
		}
		if class.Execution.Mode == config.ExecutionModePyxis && filepath.IsAbs(class.Execution.Image) {
			if _, err := os.Stat(class.Execution.Image); err != nil {
				return fmt.Errorf("class %s image: %w", class.Name, err)
			}
		}
		checks["class:"+class.Name] = "ok (" + string(class.Execution.Mode) + ")"
	}
	return json.NewEncoder(os.Stdout).Encode(checks)
}

func status(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	state, err := store.OpenReadOnly(cfg.Service.StatePath)
	if err != nil {
		return err
	}
	defer state.Close()
	leases, err := state.List()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(leases)
}

func generateKey(output string) error {
	if !filepath.IsAbs(output) {
		return errors.New("key output path must be absolute")
	}
	key, err := bootstrap.GenerateKey()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(file, key); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func newSlurmClient(cfg config.SlurmConfig) (*slurm.RESTClient, error) {
	return slurm.NewRESTClient(slurm.RESTConfig{
		BaseURL: cfg.BaseURL, APIVersion: cfg.APIVersion, User: cfg.User,
		TokenFile: cfg.TokenFile, CAFile: cfg.CAFile, Timeout: cfg.RequestTimeout,
	})
}

func slurmSmoke(args []string) error {
	flags := flag.NewFlagSet("slurm-smoke", flag.ContinueOnError)
	baseURL := flags.String("base-url", "", "slurmrestd base URL")
	apiVersion := flags.String("api-version", "v0.0.42", "Slurm REST API version")
	user := flags.String("user", "", "Slurm user")
	tokenFile := flags.String("token-file", "", "Slurm JWT file")
	partition := flags.String("partition", "defq", "Slurm partition")
	account := flags.String("account", "", "Slurm account")
	qos := flags.String("qos", "", "Slurm QOS")
	mode := flags.String("mode", string(config.ExecutionModePyxis), "execution mode: pyxis or vmocs")
	image := flags.String("image", "", "Pyxis image reference or squashfs path")
	vmImage := flags.String("vm-image", "", "VMoCS template name")
	workRoot := flags.String("work-root", "", "shared directory for smoke logs")
	cpus := flags.Int("cpus", 1, "CPUs per task")
	memoryMiB := flags.Uint64("memory-mib", 512, "memory per node in MiB")
	timeout := flags.Duration("timeout", 5*time.Minute, "maximum time to wait")
	requireRunner := flags.Bool("require-runner", false, "also verify the official runner and bootstrap in the image")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baseURL == "" || *user == "" || *tokenFile == "" || *account == "" || *workRoot == "" {
		return errors.New("slurm-smoke requires --base-url, --user, --token-file, --account, and --work-root")
	}
	if !filepath.IsAbs(*workRoot) {
		return errors.New("slurm-smoke --work-root must be absolute")
	}
	if *cpus < 1 || *memoryMiB < 1 {
		return errors.New("slurm-smoke --cpus and --memory-mib must be positive")
	}
	executionMode := config.ExecutionMode(*mode)
	switch executionMode {
	case config.ExecutionModePyxis:
		if *image == "" {
			return errors.New("slurm-smoke --mode=pyxis requires --image")
		}
	case config.ExecutionModeVMoCS:
		if *vmImage == "" {
			return errors.New("slurm-smoke --mode=vmocs requires --vm-image")
		}
	case config.ExecutionModeNative:
		return fmt.Errorf("slurm-smoke: %w", launch.ErrNativeNotImplemented)
	default:
		return fmt.Errorf("slurm-smoke: unsupported execution mode %q", executionMode)
	}
	workDir, err := os.MkdirTemp(*workRoot, "slurm-gha-smoke-")
	if err != nil {
		return err
	}
	if err := os.Chmod(workDir, 0o700); err != nil {
		return err
	}
	client, err := slurm.NewRESTClient(slurm.RESTConfig{
		BaseURL: *baseURL, APIVersion: *apiVersion, User: *user,
		TokenFile: *tokenFile, Timeout: 30 * time.Second,
	})
	if err != nil {
		return err
	}
	correlation := "slurm-gha/smoke-" + filepath.Base(workDir)
	commandArgs, environment, markers, err := buildSmokeCommand(
		executionMode, *image, *vmImage, workDir, *cpus, *requireRunner,
	)
	if err != nil {
		return err
	}
	for i := range commandArgs {
		commandArgs[i] = shellQuote(commandArgs[i])
	}
	script := "#!/usr/bin/env bash\nset -uo pipefail\n" + strings.Join(commandArgs, " ") + "\n"
	stdoutPath := filepath.Join(workDir, "slurm.out")
	stderrPath := filepath.Join(workDir, "slurm.err")
	jobID, err := client.Submit(context.Background(), slurm.SubmitRequest{
		Name: "slurm-gha-smoke", Partition: *partition, Account: *account, QOS: *qos,
		Nodes: 1, Tasks: 1, CPUsPerTask: *cpus, MemoryMiB: *memoryMiB, WallMinutes: 5,
		WorkingDirectory: workDir, StandardOutput: stdoutPath, StandardError: stderrPath,
		Correlation: correlation, Environment: environment, Script: script,
	})
	if err != nil {
		return err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = client.Cancel(context.Background(), jobID)
		}
	}()
	fmt.Printf("submitted %s smoke allocation %s; logs: %s\n", *mode, jobID.String(), workDir)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := waitForSmoke(ctx, client, jobID, stdoutPath, stderrPath, markers); err != nil {
		return err
	}
	succeeded = true
	fmt.Printf("smoke allocation %s passed %s and GitHub egress checks\n", jobID.String(), *mode)
	return nil
}

func buildSmokeCommand(mode config.ExecutionMode, image, vmImage, workDir string, cpus int, requireRunner bool) ([]string, map[string]string, []string, error) {
	marker := ""
	environment := map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	guestCheck := `getent hosts github.com >/dev/null
timeout 15 bash -c 'exec 3<>/dev/tcp/github.com/443'
printf 'GITHUB_TCP_OK\n'`
	if requireRunner {
		guestCheck += `
test -x /opt/actions-runner/bin/Runner.Listener
test -x /opt/slurm-gha/bootstrap.sh
mkdir -p /runner/runner-smoke
cp -a /opt/actions-runner/. /runner/runner-smoke/
(cd /runner/runner-smoke && ./bin/Runner.Listener --version)
printf 'RUNNER_IMAGE_OK\n'`
	}

	var commandArgs []string
	switch mode {
	case config.ExecutionModePyxis:
		if image == "" {
			return nil, nil, nil, errors.New("slurm-smoke --mode=pyxis requires --image")
		}
		marker = "PYXIS_OK"
		commandArgs = []string{
			"srun", "--nodes=1", "--ntasks=1", fmt.Sprintf("--cpus-per-task=%d", cpus), "--kill-on-bad-exit=1",
			"--container-image=" + image, "--container-mounts=" + workDir + ":/runner",
			"--container-workdir=/runner", "--no-container-mount-home", "--no-container-entrypoint",
			"/bin/bash", "-c",
		}
	case config.ExecutionModeVMoCS:
		if vmImage == "" {
			return nil, nil, nil, errors.New("slurm-smoke --mode=vmocs requires --vm-image")
		}
		marker = "VMOCS_OK"
		nonce := filepath.Base(workDir)
		environment["SLURM_GHA_SMOKE_NONCE"] = nonce
		guestCheck = fmt.Sprintf("test \"$SLURM_GHA_SMOKE_NONCE\" = %s\ntest -n \"$SLURM_JOB_ID\"\nprintf 'VMOCS_ENV_OK\\n'\n", shellQuote(nonce)) + guestCheck
		commandArgs = []string{
			"srun", "--nodes=1", "--ntasks=1", fmt.Sprintf("--cpus-per-task=%d", cpus), "--kill-on-bad-exit=1", "--export=ALL",
			"--vm-image=" + vmImage,
			"--vm-forward-env=SLURM_GHA_SMOKE_NONCE", "--vm-forward-env=SLURM_JOB_ID",
			"/bin/bash", "-c",
		}
	case config.ExecutionModeNative:
		return nil, nil, nil, fmt.Errorf("slurm-smoke: %w", launch.ErrNativeNotImplemented)
	default:
		return nil, nil, nil, fmt.Errorf("slurm-smoke: unsupported execution mode %q", mode)
	}
	guestCheck = "set -euo pipefail\n" + fmt.Sprintf("printf '%s host=%%s job=%%s\\n' \"$(hostname)\" \"${SLURM_JOB_ID:-}\"\n", marker) + guestCheck
	commandArgs = append(commandArgs, guestCheck)
	markers := []string{marker, "GITHUB_TCP_OK"}
	if mode == config.ExecutionModeVMoCS {
		markers = append(markers, "VMOCS_ENV_OK")
	}
	if requireRunner {
		markers = append(markers, "RUNNER_IMAGE_OK")
	}
	return commandArgs, environment, markers, nil
}

func waitForSmoke(ctx context.Context, client *slurm.RESTClient, id slurm.JobID, stdoutPath, stderrPath string, markers []string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	seen := false
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("smoke allocation timed out: %w", ctx.Err())
		case <-ticker.C:
			jobs, err := client.ListJobs(ctx)
			if err != nil {
				return err
			}
			found := false
			terminal := false
			for _, job := range jobs {
				if job.ID.ID == id.ID {
					found, seen = true, true
					terminal = job.Phase == slurm.PhaseTerminal
					if terminal && (job.Exit.ReturnCode == nil || *job.Exit.ReturnCode != 0) {
						stderr, _ := os.ReadFile(stderrPath)
						return fmt.Errorf("smoke allocation ended in %s: %s", job.RawState, strings.TrimSpace(string(stderr)))
					}
					break
				}
			}
			stdout, _ := os.ReadFile(stdoutPath)
			allMarkers := true
			for _, marker := range markers {
				allMarkers = allMarkers && strings.Contains(string(stdout), marker)
			}
			if allMarkers && (terminal || !found) {
				return nil
			}
			if terminal && !allMarkers {
				stderr, _ := os.ReadFile(stderrPath)
				return fmt.Errorf("smoke allocation completed without success markers; stdout: %s; stderr: %s", strings.TrimSpace(string(stdout)), strings.TrimSpace(string(stderr)))
			}
			if seen && !found && !allMarkers {
				stderr, _ := os.ReadFile(stderrPath)
				return fmt.Errorf("smoke allocation disappeared without success markers: %s", strings.TrimSpace(string(stderr)))
			}
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
