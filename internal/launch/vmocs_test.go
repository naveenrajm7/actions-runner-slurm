package launch

import (
	"strings"
	"testing"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

func TestRenderVMoCSForwardsOnlyNamedRunnerVariables(t *testing.T) {
	service := config.ServiceConfig{
		PublicURL: "https://service.example/", LogRoot: "/logs",
		TLS: config.TLSConfig{CAFile: "/host/secrets/callback-ca.crt"},
	}
	class := config.ScaleSetConfig{
		Name:  "vm-cpu",
		Slurm: config.ResourceConfig{CPUsPerTask: 2},
		Execution: config.ExecutionConfig{
			Mode: config.ExecutionModeVMoCS, Image: "base-ubuntu", ScratchRoot: "/scratch",
		},
	}
	lease := store.Lease{ID: "0123456789abcdef0123456789abcdef"}
	rendered, err := RenderVMoCS(service, class, lease, "top-secret-claim")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(rendered.Script, "top-secret-claim") {
		t.Fatal("claim credential leaked into batch script")
	}
	for _, want := range []string{
		"--cpus-per-task=2",
		"--vm-image=base-ubuntu",
		"--vm-forward-env=SLURM_GHA_SERVICE_URL",
		"--vm-forward-env=SLURM_GHA_LEASE_ID",
		"--vm-forward-env=SLURM_GHA_CLASS",
		"--vm-forward-env=SLURM_GHA_CLAIM_TOKEN",
		"--vm-forward-env=SLURM_JOB_ID",
		"--vm-forward-env=SLURM_GHA_CA_FILE",
		vmocsBootstrapPath,
	} {
		if !strings.Contains(rendered.Script, want) {
			t.Errorf("script is missing %q:\n%s", want, rendered.Script)
		}
	}
	if rendered.Environment["SLURM_GHA_CLAIM_TOKEN"] != "top-secret-claim" {
		t.Fatal("claim credential missing from Slurm job environment")
	}
	if rendered.Environment["SLURM_GHA_SERVICE_URL"] != "https://service.example" {
		t.Fatalf("service URL = %q", rendered.Environment["SLURM_GHA_SERVICE_URL"])
	}
	if rendered.Environment["SLURM_GHA_CA_FILE"] != vmocsCallbackCAPath {
		t.Fatalf("guest CA path = %q, want %q", rendered.Environment["SLURM_GHA_CA_FILE"], vmocsCallbackCAPath)
	}
	if strings.Contains(rendered.Script, service.TLS.CAFile) {
		t.Fatal("host callback CA path must not be passed to the VM")
	}
}

func TestRenderVMoCSOmitsCAForwardingForPublicCertificate(t *testing.T) {
	class := config.ScaleSetConfig{
		Name: "vm-cpu",
		Execution: config.ExecutionConfig{
			Mode: config.ExecutionModeVMoCS, Image: "base-ubuntu", ScratchRoot: "/scratch",
		},
	}
	rendered, err := RenderVMoCS(
		config.ServiceConfig{PublicURL: "https://service.example", LogRoot: "/logs"},
		class,
		store.Lease{ID: "0123456789abcdef0123456789abcdef"},
		"claim",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rendered.Environment["SLURM_GHA_CA_FILE"]; ok {
		t.Fatal("unexpected callback CA environment entry")
	}
	if strings.Contains(rendered.Script, "--vm-forward-env=SLURM_GHA_CA_FILE") {
		t.Fatal("unexpected callback CA forwarding option")
	}
}
