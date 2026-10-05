package slurm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSubmitUsesV0042WireShapeAndRotatedToken(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		wantToken := "first"
		if requests == 2 {
			wantToken = "second"
		}
		if got := r.Header.Get("X-SLURM-USER-TOKEN"); got != wantToken {
			t.Errorf("token = %q, want %q", got, wantToken)
		}
		if requests == 1 {
			return jsonResponse(http.StatusOK, `{"meta":{},"errors":[],"warnings":[]}`), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		job := body["job"].(map[string]any)
		if got := job["tres_per_node"]; got != "gres/gpu=1" {
			t.Errorf("tres_per_node = %v", got)
		}
		memory := job["memory_per_node"].(map[string]any)
		if memory["set"] != true || memory["number"] != float64(4096) {
			t.Errorf("memory_per_node = %#v", memory)
		}
		return jsonResponse(http.StatusOK, `{"job_id":123,"errors":[],"warnings":[]}`), nil
	})
	client, err := NewRESTClient(RESTConfig{BaseURL: "https://slurm.example", APIVersion: "v0.0.42", User: "ci", TokenFile: tokenPath, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = transport
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("SLURM_JWT=second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := client.Submit(context.Background(), SubmitRequest{Name: "test", Partition: "p", Account: "a", Nodes: 1, Tasks: 1, CPUsPerTask: 2, MemoryMiB: 4096, GRES: "gpu:1", WallMinutes: 10, WorkingDirectory: "/tmp", StandardOutput: "/tmp/o", StandardError: "/tmp/e", Script: "#!/bin/sh\ntrue"})
	if err != nil {
		t.Fatal(err)
	}
	if id.ID != 123 {
		t.Fatalf("job ID = %d", id.ID)
	}
}

func TestSuccessStatusWithAPIErrorsFails(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tokenPath, []byte("token"), 0o600)
	client, err := NewRESTClient(RESTConfig{BaseURL: "https://slurm.example", APIVersion: "v0.0.42", User: "ci", TokenFile: tokenPath})
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"errors":[{"error":"bad request","description":"partition missing","error_number":1}]}`), nil
	})
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping unexpectedly succeeded")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNormalizePhase(t *testing.T) {
	for state, want := range map[string]Phase{"PENDING": PhasePending, "CONFIGURING": PhaseStarting, "RUNNING": PhaseRunning, "COMPLETING": PhaseCompleting, "FAILED": PhaseTerminal, "FUTURE": PhaseUnknown} {
		got, _ := NormalizePhase([]string{state})
		if got != want {
			t.Errorf("NormalizePhase(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestListJobsV0042Fixture(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "slurm", "v0.0.42", "jobs-running.json"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewRESTClient(RESTConfig{BaseURL: "https://slurm.example", APIVersion: "v0.0.42", User: "ci", TokenFile: tokenPath})
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, string(fixture)), nil
	})
	jobs, err := client.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	job := jobs[0]
	if job.ID.ID != 12345 || job.ID.Cluster != "cluster" || job.Phase != PhaseRunning || job.Correlation != "slurm-gha/0123456789abcdef0123456789abcdef" {
		t.Fatalf("decoded job = %#v", job)
	}
	if job.Exit.ReturnCode == nil || *job.Exit.ReturnCode != 0 {
		t.Fatalf("exit code = %#v", job.Exit.ReturnCode)
	}
}
