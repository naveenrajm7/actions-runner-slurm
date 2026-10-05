package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/launch"
	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

type Controller struct {
	state   *store.Store
	slurm   slurm.Client
	service config.ServiceConfig
	classes map[string]config.ScaleSetConfig
	logger  *slog.Logger
	now     func() time.Time
	mu      sync.Mutex
}

func New(state *store.Store, slurmClient slurm.Client, service config.ServiceConfig, classes []config.ScaleSetConfig, logger *slog.Logger) (*Controller, error) {
	if state == nil || slurmClient == nil {
		return nil, errors.New("state and Slurm clients are required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	byName := make(map[string]config.ScaleSetConfig, len(classes))
	for _, class := range classes {
		if class.Execution.Mode != "pyxis" {
			return nil, fmt.Errorf("class %q uses execution mode %q; this prototype serves pyxis classes only", class.Name, class.Execution.Mode)
		}
		byName[class.Name] = class
	}
	return &Controller{state: state, slurm: slurmClient, service: service, classes: byName, logger: logger, now: time.Now}, nil
}

func (c *Controller) SetDesired(ctx context.Context, className string, desired int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	class, ok := c.classes[className]
	if !ok {
		return 0, fmt.Errorf("unknown class %q", className)
	}
	if desired < 0 {
		desired = 0
	}
	if desired > class.MaxRunners {
		desired = class.MaxRunners
	}
	leases, err := c.state.List()
	if err != nil {
		return 0, err
	}
	active, pending := filterActive(leases, className)
	if len(active) < desired {
		toCreate := desired - len(active)
		pendingSlots := class.MaxPending - pending
		if toCreate > pendingSlots {
			toCreate = pendingSlots
		}
		for range toCreate {
			if _, err := c.provisionOne(ctx, class); err != nil {
				return len(active), err
			}
			active = append(active, store.Lease{})
		}
	} else if len(active) > desired {
		if err := c.scaleDownPending(ctx, active, len(active)-desired); err != nil {
			return len(active), err
		}
	}
	return len(active), nil
}

func (c *Controller) ReconcileSlurm(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	jobs, err := c.slurm.ListJobs(ctx)
	if err != nil {
		return err
	}
	byCorrelation := make(map[string][]slurm.Job)
	byID := make(map[int64]slurm.Job)
	for _, job := range jobs {
		if job.Correlation != "" {
			byCorrelation[job.Correlation] = append(byCorrelation[job.Correlation], job)
		}
		byID[job.ID.ID] = job
	}
	leases, err := c.state.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, lease := range leases {
		if lease.State == store.StateCleaned || lease.State == store.StateTerminal {
			continue
		}
		if lease.SlurmJob.ID == 0 {
			matches := byCorrelation[lease.Correlation]
			if len(matches) == 1 && (lease.State == store.StateSubmitting || lease.State == store.StateAmbiguous) {
				if err := c.state.BindJob(lease.ID, matches[0].ID, c.now()); err != nil {
					errs = append(errs, fmt.Errorf("adopt lease %s: %w", lease.ID, err))
				}
			} else if len(matches) > 1 {
				errs = append(errs, fmt.Errorf("lease %s has %d matching Slurm allocations", lease.ID, len(matches)))
			}
			continue
		}
		job, ok := byID[lease.SlurmJob.ID]
		if !ok || job.Correlation != lease.Correlation {
			continue
		}
		if err := c.state.UpdateObservation(lease.ID, job.RawState, job.PendingReason, c.now()); err != nil {
			errs = append(errs, err)
			continue
		}
		switch job.Phase {
		case slurm.PhaseStarting, slurm.PhaseRunning:
			if lease.State == store.StatePending {
				errs = appendIf(errs, c.state.Transition(lease.ID, store.StateStarting, c.now()))
			}
		case slurm.PhaseCompleting:
			if lease.State != store.StateCompleting && lease.State != store.StateBusy {
				errs = appendIf(errs, c.state.Transition(lease.ID, store.StateCompleting, c.now()))
			}
		case slurm.PhaseTerminal:
			errs = appendIf(errs, c.state.Transition(lease.ID, store.StateTerminal, c.now()))
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) JobStarted(runnerName string, runnerID int64) error {
	_, err := c.state.MarkJobStarted(runnerName, runnerID, c.now())
	return err
}

func (c *Controller) JobCompleted(runnerName, result string) error {
	_, err := c.state.MarkJobCompleted(runnerName, result, c.now())
	return err
}

func (c *Controller) provisionOne(ctx context.Context, class config.ScaleSetConfig) (store.Lease, error) {
	snapshot, err := json.Marshal(class)
	if err != nil {
		return store.Lease{}, err
	}
	claimTTL := class.MaxQueueWait + class.StartupTimeout
	lease, token, err := c.state.CreateLease(class.Name, snapshot, claimTTL, c.now())
	if err != nil {
		return store.Lease{}, err
	}
	if err := c.state.Transition(lease.ID, store.StateSubmitting, c.now()); err != nil {
		return lease, err
	}
	rendered, err := launch.RenderPyxis(c.service, class, lease, token)
	if err != nil {
		_ = c.state.Transition(lease.ID, store.StateTerminal, c.now())
		return lease, err
	}
	if err := rendered.PrepareDirectories(); err != nil {
		_ = c.state.Transition(lease.ID, store.StateTerminal, c.now())
		return lease, err
	}
	jobID, err := c.slurm.Submit(ctx, slurm.SubmitRequest{
		Name: compactJobName(class.Name), Partition: class.Slurm.Partition, Account: class.Slurm.Account,
		QOS: class.Slurm.QOS, Nodes: class.Slurm.Nodes, Tasks: class.Slurm.Tasks,
		CPUsPerTask: class.Slurm.CPUsPerTask, MemoryMiB: class.Slurm.MemoryMiB,
		GRES: class.Slurm.GRES, WallMinutes: class.Slurm.WallMinutes,
		WorkingDirectory: rendered.WorkingDirectory, StandardOutput: rendered.StandardOutput,
		StandardError: rendered.StandardError, Correlation: lease.Correlation,
		Environment: rendered.Environment, Script: rendered.Script,
	})
	if err != nil {
		var apiErr *slurm.APIError
		if errors.As(err, &apiErr) {
			_ = c.state.Transition(lease.ID, store.StateTerminal, c.now())
		} else {
			_ = c.state.Transition(lease.ID, store.StateAmbiguous, c.now())
		}
		return lease, fmt.Errorf("submit lease %s: %w", lease.ID, err)
	}
	if err := c.state.BindJob(lease.ID, jobID, c.now()); err != nil {
		return lease, fmt.Errorf("persist Slurm job binding for lease %s (job %s): %w", lease.ID, jobID, err)
	}
	lease.SlurmJob = jobID
	lease.State = store.StatePending
	c.logger.Info("submitted runner allocation", "class", class.Name, "lease_id", lease.ID, "slurm_job", jobID.String())
	return lease, nil
}

func (c *Controller) scaleDownPending(ctx context.Context, active []store.Lease, count int) error {
	jobs, err := c.slurm.ListJobs(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]slurm.Job, len(jobs))
	for _, job := range jobs {
		byID[job.ID.ID] = job
	}
	sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt.After(active[j].CreatedAt) })
	for _, lease := range active {
		if count == 0 {
			break
		}
		if lease.State != store.StatePending || lease.SlurmJob.ID == 0 {
			continue
		}
		job, ok := byID[lease.SlurmJob.ID]
		if !ok || job.Correlation != lease.Correlation || job.Phase != slurm.PhasePending {
			continue
		}
		if err := c.slurm.Cancel(ctx, lease.SlurmJob); err != nil {
			return fmt.Errorf("cancel surplus pending lease %s: %w", lease.ID, err)
		}
		if err := c.state.Transition(lease.ID, store.StateTerminal, c.now()); err != nil {
			return err
		}
		count--
	}
	return nil
}

func filterActive(leases []store.Lease, className string) ([]store.Lease, int) {
	var active []store.Lease
	pending := 0
	for _, lease := range leases {
		if lease.ClassName != className || lease.State == store.StateTerminal || lease.State == store.StateCleaned {
			continue
		}
		active = append(active, lease)
		switch lease.State {
		case store.StateIntent, store.StateSubmitting, store.StateAmbiguous, store.StatePending, store.StateStarting:
			pending++
		}
	}
	return active, pending
}

func compactJobName(class string) string {
	name := "gha-" + strings.ToLower(class)
	if len(name) > 64 {
		return name[:64]
	}
	return name
}

func appendIf(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}
