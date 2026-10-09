package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/launch"
)

func TestBuildSmokeCommandVMoCSChecksEnvironmentForwarding(t *testing.T) {
	args, environment, markers, err := buildSmokeCommand(
		config.ExecutionModeVMoCS, "", "base-ubuntu", "/shared/smoke-123", 2, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--cpus-per-task=2",
		"--vm-image=base-ubuntu",
		"--vm-forward-env=SLURM_GHA_SMOKE_NONCE",
		"--vm-forward-env=SLURM_JOB_ID",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
	if environment["SLURM_GHA_SMOKE_NONCE"] != "smoke-123" {
		t.Fatalf("smoke nonce = %q", environment["SLURM_GHA_SMOKE_NONCE"])
	}
	guestCheck := args[len(args)-1]
	if !strings.HasPrefix(guestCheck, "set -euo pipefail\n") {
		t.Fatalf("guest check does not enable strict fail-fast before environment validation:\n%s", guestCheck)
	}
	if !strings.Contains(guestCheck, `test -n "$SLURM_JOB_ID"`) {
		t.Fatalf("guest check does not validate forwarded SLURM_JOB_ID:\n%s", guestCheck)
	}
	for _, want := range []string{"VMOCS_OK", "VMOCS_ENV_OK", "RUNNER_IMAGE_OK"} {
		if !strings.Contains(guestCheck, want) || !slices.Contains(markers, want) {
			t.Errorf("VMoCS smoke check is missing marker %q", want)
		}
	}
}

func TestBuildSmokeCommandKeepsPyxisInterface(t *testing.T) {
	args, environment, markers, err := buildSmokeCommand(
		config.ExecutionModePyxis, "/images/runner.sqsh", "", "/shared/smoke-123", 4, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "--container-image=/images/runner.sqsh") {
		t.Fatalf("Pyxis args = %q", args)
	}
	if !slices.Contains(args, "--cpus-per-task=4") {
		t.Fatalf("Pyxis args do not propagate CPUs per task: %q", args)
	}
	if _, ok := environment["SLURM_GHA_SMOKE_NONCE"]; ok {
		t.Fatal("Pyxis smoke unexpectedly sets a VMoCS nonce")
	}
	if !slices.Contains(markers, "PYXIS_OK") || slices.Contains(markers, "RUNNER_IMAGE_OK") {
		t.Fatalf("Pyxis markers = %q", markers)
	}
}

func TestBuildSmokeCommandRejectsNative(t *testing.T) {
	_, _, _, err := buildSmokeCommand(config.ExecutionModeNative, "", "", "/shared/smoke", 1, false)
	if !errors.Is(err, launch.ErrNativeNotImplemented) {
		t.Fatalf("error = %v, want ErrNativeNotImplemented", err)
	}
}
