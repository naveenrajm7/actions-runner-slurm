package launch

import (
	"errors"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

var ErrNativeNotImplemented = errors.New("native execution mode is reserved but not implemented")

type nativeBackend struct{}

func (nativeBackend) Mode() config.ExecutionMode { return config.ExecutionModeNative }
func (nativeBackend) Ready() error               { return ErrNativeNotImplemented }
func (nativeBackend) Render(config.ServiceConfig, config.ScaleSetConfig, store.Lease, string) (RenderedJob, error) {
	return RenderedJob{}, ErrNativeNotImplemented
}
