package launch

import (
	"fmt"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

// Backend is the execution-mode boundary. Slurm lifecycle management stays in
// the reconciler; each backend is responsible only for rendering its job step.
type Backend interface {
	Mode() config.ExecutionMode
	Ready() error
	Render(config.ServiceConfig, config.ScaleSetConfig, store.Lease, string) (RenderedJob, error)
}

// BackendForMode is the single registry for Slurm execution plugins.
func BackendForMode(mode config.ExecutionMode) (Backend, error) {
	switch mode {
	case config.ExecutionModePyxis:
		return pyxisBackend{}, nil
	case config.ExecutionModeVMoCS:
		return vmocsBackend{}, nil
	case config.ExecutionModeNative:
		return nativeBackend{}, nil
	default:
		return nil, fmt.Errorf("unsupported execution mode %q", mode)
	}
}
