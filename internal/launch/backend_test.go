package launch

import (
	"errors"
	"testing"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
)

func TestBackendRegistry(t *testing.T) {
	tests := []struct {
		mode        config.ExecutionMode
		wantReady   bool
		wantBackend bool
	}{
		{mode: config.ExecutionModePyxis, wantReady: true, wantBackend: true},
		{mode: config.ExecutionModeVMoCS, wantReady: true, wantBackend: true},
		{mode: config.ExecutionModeNative, wantBackend: true},
		{mode: "unknown"},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			backend, err := BackendForMode(test.mode)
			if (backend != nil) != test.wantBackend {
				t.Fatalf("backend = %T, wantBackend = %t (error: %v)", backend, test.wantBackend, err)
			}
			if !test.wantBackend {
				if err == nil {
					t.Fatal("unknown mode unexpectedly resolved")
				}
				return
			}
			if backend.Mode() != test.mode {
				t.Fatalf("Mode() = %q, want %q", backend.Mode(), test.mode)
			}
			readyErr := backend.Ready()
			if test.wantReady && readyErr != nil {
				t.Fatalf("Ready() = %v", readyErr)
			}
			if test.mode == config.ExecutionModeNative && !errors.Is(readyErr, ErrNativeNotImplemented) {
				t.Fatalf("native Ready() = %v, want ErrNativeNotImplemented", readyErr)
			}
		})
	}
}
