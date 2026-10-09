package launch

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

const (
	vmocsBootstrapPath  = "/opt/slurm-gha/bootstrap.sh"
	vmocsCallbackCAPath = "/etc/slurm-gha/callback-ca.crt"
)

var vmocsForwardEnvironment = []string{
	"SLURM_GHA_SERVICE_URL",
	"SLURM_GHA_LEASE_ID",
	"SLURM_GHA_CLASS",
	"SLURM_GHA_CLAIM_TOKEN",
	"SLURM_JOB_ID",
}

type vmocsBackend struct{}

func (vmocsBackend) Mode() config.ExecutionMode { return config.ExecutionModeVMoCS }
func (vmocsBackend) Ready() error               { return nil }
func (vmocsBackend) Render(service config.ServiceConfig, class config.ScaleSetConfig, lease store.Lease, claimToken string) (RenderedJob, error) {
	return RenderVMoCS(service, class, lease, claimToken)
}

// RenderVMoCS builds an srun step that asks the VMoCS SPANK plugin to boot a
// template and execute the runner bootstrap inside the guest VM.
func RenderVMoCS(service config.ServiceConfig, class config.ScaleSetConfig, lease store.Lease, claimToken string) (RenderedJob, error) {
	if class.Execution.Mode != config.ExecutionModeVMoCS {
		return RenderedJob{}, errors.New("VMoCS renderer requires execution.mode=vmocs")
	}
	if lease.ID == "" || claimToken == "" {
		return RenderedJob{}, errors.New("lease and claim credential are required")
	}
	if strings.ContainsAny(class.Execution.Image, "\n\r") {
		return RenderedJob{}, errors.New("VMoCS template name must not contain newlines")
	}

	leaseRoot := filepath.Join(class.Execution.ScratchRoot, "slurm-gha", lease.ID)
	logDir := filepath.Join(service.LogRoot, class.Name, lease.ID)
	forward := append([]string(nil), vmocsForwardEnvironment...)
	environment := map[string]string{
		"PATH":                  "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SLURM_GHA_SERVICE_URL": strings.TrimSuffix(service.PublicURL, "/"),
		"SLURM_GHA_LEASE_ID":    lease.ID,
		"SLURM_GHA_CLASS":       class.Name,
		"SLURM_GHA_CLAIM_TOKEN": claimToken,
	}
	if service.TLS.CAFile != "" {
		environment["SLURM_GHA_CA_FILE"] = vmocsCallbackCAPath
		forward = append(forward, "SLURM_GHA_CA_FILE")
	}

	args := []string{
		"srun", "--nodes=1", "--ntasks=1",
		fmt.Sprintf("--cpus-per-task=%d", class.Slurm.CPUsPerTask),
		"--kill-on-bad-exit=1", "--export=ALL",
		"--vm-image=" + class.Execution.Image,
	}
	for _, name := range forward {
		args = append(args, "--vm-forward-env="+name)
	}
	args = append(args, vmocsBootstrapPath)
	for i := range args {
		args[i] = shellQuote(args[i])
	}

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -uo pipefail
umask 077

lease_root=%s
log_dir=%s

mkdir -p -- "$lease_root" "$log_dir"
chmod 0700 -- "$lease_root" "$log_dir"

status=0
%s || status=$?

case "$lease_root" in
  %s/slurm-gha/%s) rm -rf -- "$lease_root" ;;
  *) printf 'refusing unsafe scratch cleanup: %%s\n' "$lease_root" >&2; status=125 ;;
esac
exit "$status"
`, shellQuote(leaseRoot), shellQuote(logDir), strings.Join(args, " "), shellQuote(class.Execution.ScratchRoot), lease.ID)

	return RenderedJob{
		Script: script, WorkingDirectory: class.Execution.ScratchRoot,
		StandardOutput: filepath.Join(logDir, "slurm.out"), StandardError: filepath.Join(logDir, "slurm.err"),
		ScratchDirectory: leaseRoot, LogDirectory: logDir,
		Environment: environment,
	}, nil
}
