package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
	"github.com/naveenrajm7/actions-runner-slurm/internal/store"
)

type fakeSlurm struct{ jobs []slurm.Job }

func (f *fakeSlurm) Ping(context.Context) error { return nil }
func (f *fakeSlurm) Submit(context.Context, slurm.SubmitRequest) (slurm.JobID, error) {
	return slurm.JobID{}, nil
}
func (f *fakeSlurm) ListJobs(context.Context) ([]slurm.Job, error) { return f.jobs, nil }
func (f *fakeSlurm) Cancel(context.Context, slurm.JobID) error     { return nil }

type fakeProvider struct {
	calls int
	jit   string
}

func (p *fakeProvider) GenerateJIT(context.Context, string, string) (string, error) {
	p.calls++
	return p.jit, nil
}

func TestClaimIsVerifiedAndIdempotent(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	lease, token, err := state.CreateLease("cpu", nil, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Transition(lease.ID, store.StateSubmitting, now); err != nil {
		t.Fatal(err)
	}
	jobID := slurm.JobID{Cluster: "cluster", ID: 123}
	if err := state.BindJob(lease.ID, jobID, now); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "key")
	key, _ := GenerateKey()
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := LoadSealer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{jit: "sensitive-jit"}
	slurmClient := &fakeSlurm{jobs: []slurm.Job{{ID: jobID, Correlation: lease.Correlation, Phase: slurm.PhaseRunning, RawState: "RUNNING"}}}
	handler, err := NewHandler(state, slurmClient, provider, sealer)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now.Add(time.Minute) }
	requestBody, _ := json.Marshal(claimRequest{JobID: 123, Cluster: "cluster"})
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/leases/"+lease.ID+"/claim", bytes.NewReader(requestBody))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.String() != "sensitive-jit" {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	}
	if provider.calls != 1 {
		t.Fatalf("GenerateJIT calls = %d", provider.calls)
	}
	stored, err := state.Get(lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.JITCiphertext, []byte("sensitive-jit")) {
		t.Fatal("JIT was stored in plaintext")
	}
}

func TestClaimRejectsWrongAllocation(t *testing.T) {
	now := time.Now().UTC()
	state, _ := store.Open(filepath.Join(t.TempDir(), "state.db"))
	defer state.Close()
	lease, token, _ := state.CreateLease("cpu", nil, time.Hour, now)
	_ = state.Transition(lease.ID, store.StateSubmitting, now)
	_ = state.BindJob(lease.ID, slurm.JobID{ID: 123}, now)
	keyPath := filepath.Join(t.TempDir(), "key")
	key, _ := GenerateKey()
	_ = os.WriteFile(keyPath, []byte(key), 0o600)
	sealer, _ := LoadSealer(keyPath)
	handler, _ := NewHandler(state, &fakeSlurm{}, &fakeProvider{jit: "jit"}, sealer)
	body, _ := json.Marshal(claimRequest{JobID: 999})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/leases/"+lease.ID+"/claim", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
}
