package launch

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

type pyxisBackend struct{}

func (pyxisBackend) Mode() config.ExecutionMode { return config.ExecutionModePyxis }
func (pyxisBackend) Ready() error               { return nil }
func (pyxisBackend) Render(service config.ServiceConfig, class config.ScaleSetConfig, lease store.Lease, claimToken string) (RenderedJob, error) {
	return RenderPyxis(service, class, lease, claimToken)
}

func RenderPyxis(service config.ServiceConfig, class config.ScaleSetConfig, lease store.Lease, claimToken string) (RenderedJob, error) {
	if class.Execution.Mode != config.ExecutionModePyxis {
		return RenderedJob{}, errors.New("Pyxis renderer requires execution.mode=pyxis")
	}
	if lease.ID == "" || claimToken == "" {
		return RenderedJob{}, errors.New("lease and claim credential are required")
	}
	leaseRoot := filepath.Join(class.Execution.ScratchRoot, "slurm-gha", lease.ID)
	logDir := filepath.Join(service.LogRoot, class.Name, lease.ID)
	mounts := []string{leaseRoot + ":/runner", logDir + ":/runner-logs"}
	if service.TLS.CAFile != "" {
		mounts = append(mounts, service.TLS.CAFile+":/etc/slurm-gha/callback-ca.crt:ro")
	}
	for _, mount := range class.Execution.ExtraMounts {
		entry := mount.Source + ":" + mount.Destination
		if mount.ReadOnly {
			entry += ":ro"
		}
		mounts = append(mounts, entry)
	}
	for _, value := range append([]string{class.Execution.Image}, mounts...) {
		if strings.ContainsAny(value, "\n\r") {
			return RenderedJob{}, errors.New("image and mount values must not contain newlines")
		}
	}
	homeFlag := "--no-container-mount-home"
	if class.Execution.MountHome {
		homeFlag = "--container-mount-home"
	}
	args := []string{
		"srun", "--nodes=1", "--ntasks=1",
		fmt.Sprintf("--cpus-per-task=%d", class.Slurm.CPUsPerTask),
		"--kill-on-bad-exit=1", "--export=ALL",
		"--container-image=" + class.Execution.Image,
		"--container-mounts=" + strings.Join(mounts, ","),
		"--container-workdir=/runner", homeFlag, "--no-container-entrypoint",
		"/opt/slurm-gha/bootstrap.sh",
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
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

if [ -d "$lease_root/runner/_diag" ]; then
  cp -a -- "$lease_root/runner/_diag/." "$log_dir/" 2>/dev/null || true
fi

case "$lease_root" in
  %s/slurm-gha/%s) rm -rf -- "$lease_root" ;;
  *) printf 'refusing unsafe scratch cleanup: %%s\n' "$lease_root" >&2; status=125 ;;
esac
exit "$status"
`, shellQuote(leaseRoot), shellQuote(logDir), strings.Join(quoted, " "), shellQuote(class.Execution.ScratchRoot), lease.ID)
	environment := map[string]string{
		"PATH":                  "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SLURM_GHA_SERVICE_URL": strings.TrimSuffix(service.PublicURL, "/"),
		"SLURM_GHA_LEASE_ID":    lease.ID,
		"SLURM_GHA_CLASS":       class.Name,
		"SLURM_GHA_CLAIM_TOKEN": claimToken,
	}
	if service.TLS.CAFile != "" {
		environment["SLURM_GHA_CA_FILE"] = "/etc/slurm-gha/callback-ca.crt"
	}
	return RenderedJob{
		Script: script, WorkingDirectory: class.Execution.ScratchRoot,
		StandardOutput: filepath.Join(logDir, "slurm.out"), StandardError: filepath.Join(logDir, "slurm.err"),
		ScratchDirectory: leaseRoot, LogDirectory: logDir,
		Environment: environment,
	}, nil
}
