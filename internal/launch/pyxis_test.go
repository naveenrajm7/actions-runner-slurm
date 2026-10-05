package launch

import (
	"strings"
	"testing"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

func TestRenderPyxisDoesNotPutSecretsInScript(t *testing.T) {
	service := config.ServiceConfig{
		PublicURL: "https://service.example", LogRoot: "/logs",
		TLS: config.TLSConfig{CAFile: "/secrets/callback-ca.crt"},
	}
	class := config.ScaleSetConfig{Name: "cpu", Execution: config.ExecutionConfig{Mode: "pyxis", Image: "/cluster/images/runner.sqsh", ScratchRoot: "/scratch"}}
	lease := store.Lease{ID: "0123456789abcdef0123456789abcdef"}
	rendered, err := RenderPyxis(service, class, lease, "top-secret-claim")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Script, "top-secret-claim") {
		t.Fatal("claim credential leaked into batch script")
	}
	if !strings.Contains(rendered.Script, "--no-container-mount-home") || !strings.Contains(rendered.Script, "--no-container-entrypoint") {
		t.Fatalf("script does not set explicit Pyxis isolation flags:\n%s", rendered.Script)
	}
	if rendered.Environment["SLURM_GHA_CLAIM_TOKEN"] != "top-secret-claim" {
		t.Fatal("claim credential missing from job environment")
	}
	if rendered.Environment["SLURM_GHA_CA_FILE"] != "/etc/slurm-gha/callback-ca.crt" {
		t.Fatal("callback CA path missing from job environment")
	}
	if !strings.Contains(rendered.Script, "/secrets/callback-ca.crt:/etc/slurm-gha/callback-ca.crt:ro") {
		t.Fatal("callback CA is not mounted read-only")
	}
}
