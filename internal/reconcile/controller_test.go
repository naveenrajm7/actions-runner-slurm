package reconcile

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

type fakeSlurm struct {
	submissions []slurm.SubmitRequest
	jobs        []slurm.Job
}

func (f *fakeSlurm) Ping(context.Context) error { return nil }
func (f *fakeSlurm) Submit(_ context.Context, req slurm.SubmitRequest) (slurm.JobID, error) {
	f.submissions = append(f.submissions, req)
	return slurm.JobID{Cluster: "test", ID: int64(100 + len(f.submissions))}, nil
}
func (f *fakeSlurm) ListJobs(context.Context) ([]slurm.Job, error) { return f.jobs, nil }
func (f *fakeSlurm) Cancel(context.Context, slurm.JobID) error     { return nil }

func TestDesiredCapacityCountsPendingLeases(t *testing.T) {
	root := t.TempDir()
	state, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	client := &fakeSlurm{}
	class := config.ScaleSetConfig{
		Name: "cpu", MaxRunners: 3, MaxPending: 2, MaxQueueWait: time.Hour, StartupTimeout: time.Minute,
		Slurm:     config.ResourceConfig{Partition: "p", Account: "a", Nodes: 1, Tasks: 1, CPUsPerTask: 2, MemoryMiB: 1024, WallMinutes: 10},
		Execution: config.ExecutionConfig{Mode: "pyxis", Image: "/image.sqsh", ScratchRoot: filepath.Join(root, "scratch")},
	}
	controller, err := New(state, client, config.ServiceConfig{PublicURL: "https://service.example", LogRoot: filepath.Join(root, "logs")}, []config.ScaleSetConfig{class}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := controller.SetDesired(context.Background(), "cpu", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 || len(client.submissions) != 2 {
		t.Fatalf("live=%d submissions=%d, want 2 due to maxPending", got, len(client.submissions))
	}
	got, err = controller.SetDesired(context.Background(), "cpu", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 || len(client.submissions) != 2 {
		t.Fatalf("pending leases were not counted: live=%d submissions=%d", got, len(client.submissions))
	}
}
