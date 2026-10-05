package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

type JITProvider interface {
	GenerateJIT(context.Context, string, string) (string, error)
}

type Handler struct {
	store    *store.Store
	slurm    slurm.Client
	provider JITProvider
	sealer   *Sealer
	now      func() time.Time
	locks    sync.Map
	limiter  *attemptLimiter
}

type claimRequest struct {
	JobID   int64  `json:"jobId"`
	Cluster string `json:"cluster,omitempty"`
}

func NewHandler(state *store.Store, slurmClient slurm.Client, provider JITProvider, sealer *Sealer) (*Handler, error) {
	if state == nil || slurmClient == nil || provider == nil || sealer == nil {
		return nil, errors.New("bootstrap handler dependencies are required")
	}
	return &Handler{store: state, slurm: slurmClient, provider: provider, sealer: sealer, now: time.Now, limiter: newAttemptLimiter(10, time.Minute)}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	leaseID, ok := parseClaimPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	remote := remoteIP(r)
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		if !h.limiter.Allow(remote, h.now()) {
			http.Error(w, "too many claim attempts", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	lease, err := h.store.AuthenticateClaim(leaseID, token, h.now())
	if err != nil {
		if !h.limiter.Allow(remote, h.now()) {
			http.Error(w, "too many claim attempts", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var request claimRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil || request.JobID <= 0 {
		http.Error(w, "invalid claim request", http.StatusBadRequest)
		return
	}
	if lease.SlurmJob.ID == 0 {
		http.Error(w, "allocation binding is not ready; retry", http.StatusConflict)
		return
	}
	if request.JobID != lease.SlurmJob.ID || (lease.SlurmJob.Cluster != "" && request.Cluster != "" && request.Cluster != lease.SlurmJob.Cluster) {
		http.Error(w, "allocation does not match claim", http.StatusForbidden)
		return
	}
	job, err := h.verifiedJob(r.Context(), lease)
	if err != nil {
		http.Error(w, "allocation is not ready or cannot be verified", http.StatusConflict)
		return
	}
	if lease.State == store.StatePending {
		if err := h.store.Transition(lease.ID, store.StateStarting, h.now()); err != nil {
			http.Error(w, "lease transition failed", http.StatusConflict)
			return
		}
	}
	_ = h.store.UpdateObservation(lease.ID, job.RawState, job.PendingReason, h.now())

	lockValue, _ := h.locks.LoadOrStore(lease.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	lease, err = h.store.Get(lease.ID)
	if err != nil {
		http.Error(w, "lease unavailable", http.StatusConflict)
		return
	}
	if len(lease.JITCiphertext) > 0 {
		jit, err := h.sealer.Open(lease.ID, lease.JITCiphertext)
		if err != nil {
			http.Error(w, "cached JIT configuration is unavailable", http.StatusInternalServerError)
			return
		}
		writeJIT(w, jit)
		return
	}
	jit, err := h.provider.GenerateJIT(r.Context(), lease.ClassName, lease.RunnerName)
	if err != nil {
		http.Error(w, "JIT configuration is temporarily unavailable", http.StatusBadGateway)
		return
	}
	if jit == "" || len(jit) > 2<<20 {
		http.Error(w, "JIT provider returned invalid content", http.StatusBadGateway)
		return
	}
	ciphertext, err := h.sealer.Seal(lease.ID, []byte(jit))
	if err != nil {
		http.Error(w, "failed to protect JIT configuration", http.StatusInternalServerError)
		return
	}
	storedCiphertext, stored, err := h.store.SaveJITIfAbsent(lease.ID, ciphertext, h.now())
	if err != nil {
		http.Error(w, "failed to persist JIT configuration", http.StatusInternalServerError)
		return
	}
	if !stored {
		plain, err := h.sealer.Open(lease.ID, storedCiphertext)
		if err != nil {
			http.Error(w, "cached JIT configuration is unavailable", http.StatusInternalServerError)
			return
		}
		writeJIT(w, plain)
		return
	}
	writeJIT(w, []byte(jit))
}

func (h *Handler) verifiedJob(ctx context.Context, lease store.Lease) (slurm.Job, error) {
	jobs, err := h.slurm.ListJobs(ctx)
	if err != nil {
		return slurm.Job{}, err
	}
	for _, job := range jobs {
		if job.ID.ID != lease.SlurmJob.ID {
			continue
		}
		if lease.SlurmJob.Cluster != "" && job.ID.Cluster != "" && job.ID.Cluster != lease.SlurmJob.Cluster {
			continue
		}
		if job.Correlation != lease.Correlation || job.RestartCount != 0 {
			return slurm.Job{}, errors.New("allocation ownership or restart count mismatch")
		}
		if job.Phase != slurm.PhaseRunning && job.Phase != slurm.PhaseStarting {
			return slurm.Job{}, fmt.Errorf("allocation phase is %s", job.Phase)
		}
		return job, nil
	}
	return slurm.Job{}, errors.New("allocation not found")
}

func parseClaimPath(value string) (string, bool) {
	const prefix = "/api/v1/leases/"
	const suffix = "/claim"
	if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(value, prefix), suffix)
	if len(id) != 32 || strings.Contains(id, "/") {
		return "", false
	}
	if _, err := strconv.ParseUint(id[:16], 16, 64); err != nil {
		return "", false
	}
	if _, err := strconv.ParseUint(id[16:], 16, 64); err != nil {
		return "", false
	}
	return id, true
}

func bearerToken(value string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return "", false
	}
	token := value[len(prefix):]
	return token, !strings.ContainsAny(token, " \t\r\n")
}

func writeJIT(w http.ResponseWriter, jit []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(jit)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jit)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type attemptWindow struct {
	start time.Time
	count int
}

type attemptLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	byIP   map[string]attemptWindow
}

func newAttemptLimiter(max int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{max: max, window: window, byIP: make(map[string]attemptWindow)}
}

func (l *attemptLimiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byIP[ip]
	if w.start.IsZero() || now.Sub(w.start) >= l.window {
		l.byIP[ip] = attemptWindow{start: now, count: 1}
		return true
	}
	if w.count >= l.max {
		return false
	}
	w.count++
	l.byIP[ip] = w
	return true
}
