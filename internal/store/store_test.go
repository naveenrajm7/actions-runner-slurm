package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
)

func TestClaimIsHashedAndExpires(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	lease, token, err := s.CreateLease("cpu", []byte(`{"version":1}`), time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ClaimHash == token || lease.ClaimHash == "" {
		t.Fatal("claim token was not hashed")
	}
	if _, err := s.AuthenticateClaim(lease.ID, token, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateClaim(lease.ID, token+"bad", now); err == nil {
		t.Fatal("invalid claim succeeded")
	}
	if _, err := s.AuthenticateClaim(lease.ID, token, now.Add(time.Hour)); err == nil {
		t.Fatal("expired claim succeeded")
	}
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o", info.Mode().Perm())
	}
}

func TestTransitionsAndJobBinding(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	lease, _, _ := s.CreateLease("cpu", nil, time.Hour, now)
	if err := s.Transition(lease.ID, StateSubmitting, now); err != nil {
		t.Fatal(err)
	}
	if err := s.BindJob(lease.ID, slurm.JobID{Cluster: "cluster", ID: 42}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(lease.ID, StateBusy, now); err == nil {
		t.Fatal("invalid pending -> busy transition succeeded")
	}
	if err := s.Transition(lease.ID, StateStarting, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(lease.ID, StateRegistered, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateRegistered || got.ClaimHash != "" {
		t.Fatalf("lease = %#v", got)
	}
	if _, err := s.AuthenticateClaim(lease.ID, "anything", now); !errors.Is(err, os.ErrNotExist) && err == nil {
		t.Fatal("revoked claim succeeded")
	}
}
