package slurm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type JobID struct {
	Cluster string `json:"cluster"`
	ID      int64  `json:"id"`
}

func (id JobID) String() string {
	if id.Cluster == "" {
		return fmt.Sprintf("%d", id.ID)
	}
	return fmt.Sprintf("%s:%d", id.Cluster, id.ID)
}

type Phase string

const (
	PhasePending    Phase = "pending"
	PhaseStarting   Phase = "starting"
	PhaseRunning    Phase = "running"
	PhaseCompleting Phase = "completing"
	PhaseTerminal   Phase = "terminal"
	PhaseUnknown    Phase = "unknown"
)

type ExitCause struct {
	State      string `json:"state"`
	ReturnCode *int64 `json:"returnCode,omitempty"`
	Signal     string `json:"signal,omitempty"`
}

type Job struct {
	ID               JobID     `json:"id"`
	Name             string    `json:"name"`
	User             string    `json:"user"`
	Account          string    `json:"account"`
	Partition        string    `json:"partition"`
	Correlation      string    `json:"correlation"`
	Phase            Phase     `json:"phase"`
	RawState         string    `json:"rawState"`
	PendingReason    string    `json:"pendingReason,omitempty"`
	StateDescription string    `json:"stateDescription,omitempty"`
	Nodes            string    `json:"nodes,omitempty"`
	Requeue          bool      `json:"requeue"`
	RestartCount     int       `json:"restartCount"`
	SubmittedAt      time.Time `json:"submittedAt,omitempty"`
	StartedAt        time.Time `json:"startedAt,omitempty"`
	EndedAt          time.Time `json:"endedAt,omitempty"`
	Exit             ExitCause `json:"exit"`
}

type SubmitRequest struct {
	Name             string
	Partition        string
	Account          string
	QOS              string
	Nodes            int
	Tasks            int
	CPUsPerTask      int
	MemoryMiB        uint64
	GRES             string
	WallMinutes      uint32
	WorkingDirectory string
	StandardOutput   string
	StandardError    string
	Correlation      string
	Environment      map[string]string
	Script           string
}

type Client interface {
	Ping(context.Context) error
	Submit(context.Context, SubmitRequest) (JobID, error)
	ListJobs(context.Context) ([]Job, error)
	Cancel(context.Context, JobID) error
}

func NormalizePhase(states []string) (Phase, string) {
	if len(states) == 0 {
		return PhaseUnknown, ""
	}
	state := strings.ToUpper(states[0])
	switch state {
	case "PENDING", "REQUEUE_FED", "REQUEUE_HOLD", "REQUEUED", "RESV_DEL_HOLD", "SPECIAL_EXIT":
		return PhasePending, state
	case "CONFIGURING":
		return PhaseStarting, state
	case "RUNNING", "RESIZING", "SUSPENDED":
		return PhaseRunning, state
	case "COMPLETING", "STAGE_OUT":
		return PhaseCompleting, state
	case "BOOT_FAIL", "CANCELLED", "COMPLETED", "DEADLINE", "FAILED", "NODE_FAIL", "OUT_OF_MEMORY", "PREEMPTED", "REVOKED", "TIMEOUT":
		return PhaseTerminal, state
	default:
		return PhaseUnknown, state
	}
}
