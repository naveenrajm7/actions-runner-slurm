package slurm

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RESTConfig struct {
	BaseURL    string
	APIVersion string
	User       string
	TokenFile  string
	CAFile     string
	Timeout    time.Duration
}

type RESTClient struct {
	base       *url.URL
	apiVersion string
	user       string
	tokenFile  string
	http       *http.Client
}

type apiIssue struct {
	Description string `json:"description"`
	Number      int64  `json:"error_number"`
	ErrorText   string `json:"error"`
	Source      string `json:"source"`
}

type responseEnvelope struct {
	Errors   []apiIssue `json:"errors"`
	Warnings []apiIssue `json:"warnings"`
}

type APIError struct {
	Status int
	Issues []apiIssue
}

func (e *APIError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		message := issue.ErrorText
		if issue.Description != "" {
			message += ": " + issue.Description
		}
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("slurm REST returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("slurm REST returned HTTP %d: %s", e.Status, strings.Join(parts, "; "))
}

func NewRESTClient(cfg RESTConfig) (*RESTClient, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, errors.New("slurm REST base URL is invalid")
	}
	if !strings.HasPrefix(cfg.APIVersion, "v0.0.") {
		return nil, errors.New("unsupported Slurm REST API version")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read Slurm CA: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system CA pool: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("Slurm CA file contains no certificates")
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &RESTClient{
		base:       base,
		apiVersion: cfg.APIVersion,
		user:       cfg.User,
		tokenFile:  cfg.TokenFile,
		http:       &http.Client{Transport: transport, Timeout: cfg.Timeout},
	}, nil
}

func (c *RESTClient) Ping(ctx context.Context) error {
	var response responseEnvelope
	return c.do(ctx, http.MethodGet, c.endpoint("ping/"), nil, &response)
}

type noValue[T ~uint32 | ~uint64] struct {
	Set      bool `json:"set"`
	Infinite bool `json:"infinite"`
	Number   T    `json:"number"`
}

type jobDescriptor struct {
	Name                    string          `json:"name"`
	Partition               string          `json:"partition"`
	Account                 string          `json:"account"`
	QOS                     string          `json:"qos,omitempty"`
	Nodes                   string          `json:"nodes"`
	Tasks                   int             `json:"tasks"`
	TasksPerNode            int             `json:"tasks_per_node"`
	CPUsPerTask             int             `json:"cpus_per_task"`
	MemoryPerNode           noValue[uint64] `json:"memory_per_node"`
	TRESPerNode             string          `json:"tres_per_node,omitempty"`
	TimeLimit               noValue[uint32] `json:"time_limit"`
	CurrentWorkingDirectory string          `json:"current_working_directory"`
	StandardOutput          string          `json:"standard_output"`
	StandardError           string          `json:"standard_error"`
	Comment                 string          `json:"comment"`
	Environment             []string        `json:"environment"`
	Script                  string          `json:"script"`
	Requeue                 bool            `json:"requeue"`
	KillOnNodeFail          bool            `json:"kill_on_node_fail"`
}

type submitBody struct {
	Job jobDescriptor `json:"job"`
}

type submitResponse struct {
	responseEnvelope
	JobID int64 `json:"job_id"`
}

func (c *RESTClient) Submit(ctx context.Context, request SubmitRequest) (JobID, error) {
	if request.Nodes != 1 || request.Tasks != 1 {
		return JobID{}, errors.New("v1alpha1 requires exactly one node and one task")
	}
	env := make([]string, 0, len(request.Environment))
	for k, v := range request.Environment {
		if strings.ContainsAny(k, "=\x00\n") || k == "" || strings.ContainsRune(v, '\x00') {
			return JobID{}, fmt.Errorf("invalid environment entry %q", k)
		}
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	body := submitBody{Job: jobDescriptor{
		Name: request.Name, Partition: request.Partition, Account: request.Account, QOS: request.QOS,
		Nodes: strconv.Itoa(request.Nodes), Tasks: request.Tasks, TasksPerNode: 1, CPUsPerTask: request.CPUsPerTask,
		MemoryPerNode:           noValue[uint64]{Set: true, Number: request.MemoryMiB},
		TRESPerNode:             gresToTRES(request.GRES),
		TimeLimit:               noValue[uint32]{Set: true, Number: request.WallMinutes},
		CurrentWorkingDirectory: request.WorkingDirectory,
		StandardOutput:          request.StandardOutput, StandardError: request.StandardError,
		Comment: request.Correlation, Environment: env, Script: request.Script,
		Requeue: false, KillOnNodeFail: true,
	}}
	var response submitResponse
	if err := c.do(ctx, http.MethodPost, c.endpoint("job/submit"), body, &response); err != nil {
		return JobID{}, err
	}
	if response.JobID <= 0 {
		return JobID{}, errors.New("Slurm accepted submission without returning a job ID")
	}
	return JobID{ID: response.JobID}, nil
}

type wireJob struct {
	JobID            int64               `json:"job_id"`
	Name             string              `json:"name"`
	UserName         string              `json:"user_name"`
	Account          string              `json:"account"`
	Partition        string              `json:"partition"`
	Cluster          string              `json:"cluster"`
	Comment          string              `json:"comment"`
	Extra            string              `json:"extra"`
	JobState         []string            `json:"job_state"`
	StateReason      string              `json:"state_reason"`
	StateDescription string              `json:"state_description"`
	Nodes            string              `json:"nodes"`
	Requeue          bool                `json:"requeue"`
	RestartCount     int                 `json:"restart_cnt"`
	SubmitTime       noValue[uint64]     `json:"submit_time"`
	StartTime        noValue[uint64]     `json:"start_time"`
	EndTime          noValue[uint64]     `json:"end_time"`
	ExitCode         wireProcessExitCode `json:"exit_code"`
}

type wireProcessExitCode struct {
	Status     []string        `json:"status"`
	ReturnCode noValue[uint64] `json:"return_code"`
	Signal     struct {
		ID   noValue[uint32] `json:"id"`
		Name string          `json:"name"`
	} `json:"signal"`
}

type jobsResponse struct {
	responseEnvelope
	Jobs []wireJob `json:"jobs"`
}

type cancelBody struct {
	Jobs   []string `json:"jobs"`
	Signal string   `json:"signal"`
}

type cancelResponse struct {
	responseEnvelope
	Status []cancelStatus `json:"status"`
}

type cancelStatus struct {
	Error struct {
		String  string `json:"string"`
		Code    int32  `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	StepID     string           `json:"step_id"`
	JobID      noValue[uint32]  `json:"job_id"`
	Federation cancelFederation `json:"federation"`
}

type cancelFederation struct {
	Sibling string `json:"sibling"`
}

func (c *RESTClient) ListJobs(ctx context.Context) ([]Job, error) {
	var response jobsResponse
	if err := c.do(ctx, http.MethodGet, c.endpoint("jobs/"), nil, &response); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(response.Jobs))
	for _, item := range response.Jobs {
		phase, raw := NormalizePhase(item.JobState)
		job := Job{
			ID: JobID{Cluster: item.Cluster, ID: item.JobID}, Name: item.Name, User: item.UserName,
			Account: item.Account, Partition: item.Partition, Correlation: item.Comment,
			Phase: phase, RawState: raw, PendingReason: item.StateReason,
			StateDescription: item.StateDescription, Nodes: item.Nodes, Requeue: item.Requeue,
			RestartCount: item.RestartCount, Exit: ExitCause{State: raw, Signal: item.ExitCode.Signal.Name},
		}
		if job.Correlation == "" {
			job.Correlation = item.Extra
		}
		job.SubmittedAt = unixTime(item.SubmitTime)
		job.StartedAt = unixTime(item.StartTime)
		job.EndedAt = unixTime(item.EndTime)
		if item.ExitCode.ReturnCode.Set {
			rc := int64(item.ExitCode.ReturnCode.Number)
			job.Exit.ReturnCode = &rc
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (c *RESTClient) Cancel(ctx context.Context, id JobID) error {
	if id.ID <= 0 {
		return errors.New("invalid Slurm job ID")
	}
	// The singular DELETE /job/{id} handler rejects federation-encoded IDs
	// above MAX_JOB_ID before contacting slurmctld. The plural handler accepts
	// an explicit job list and delegates to the federation-aware kill path.
	// Unlike the singular endpoint, it requires an explicit signal.
	jobID := strconv.FormatInt(id.ID, 10)
	body := cancelBody{Jobs: []string{jobID}, Signal: "SIGKILL"}
	var response cancelResponse
	if err := c.do(ctx, http.MethodDelete, c.endpoint("jobs/"), body, &response); err != nil {
		return err
	}
	for _, result := range response.Status {
		if result.Error.Code == 0 {
			continue
		}
		target := result.StepID
		if target == "" && result.JobID.Set {
			target = strconv.FormatUint(uint64(result.JobID.Number), 10)
		}
		if target == "" {
			target = jobID
		}
		message := result.Error.Message
		if message == "" {
			message = result.Error.String
		}
		if message == "" {
			message = "unknown Slurm error"
		}
		if result.Federation.Sibling != "" {
			target += " on federation sibling " + result.Federation.Sibling
		}
		return fmt.Errorf("cancel Slurm job %s: %s (code %d)", target, message, result.Error.Code)
	}
	return nil
}

func (c *RESTClient) endpoint(suffix string) string {
	u := *c.base
	u.Path = path.Join(strings.TrimSuffix(c.base.Path, "/"), "slurm", c.apiVersion, suffix)
	if strings.HasSuffix(suffix, "/") {
		u.Path += "/"
	}
	return u.String()
}

func (c *RESTClient) do(ctx context.Context, method, target string, body, output any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Slurm request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("build Slurm request: %w", err)
	}
	token, err := c.readToken()
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SLURM-USER-NAME", c.user)
	req.Header.Set("X-SLURM-USER-TOKEN", token)
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Slurm REST request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("read Slurm REST response: %w", err)
	}
	var envelope responseEnvelope
	if len(data) > 0 {
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("decode Slurm REST envelope (HTTP %d): %w", response.StatusCode, err)
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || len(envelope.Errors) > 0 {
		return &APIError{Status: response.StatusCode, Issues: envelope.Errors}
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode Slurm REST response: %w", err)
		}
	}
	return nil
}

func (c *RESTClient) readToken() (string, error) {
	info, err := os.Stat(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("stat Slurm token: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Slurm token must not be group- or world-accessible")
	}
	b, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return "", fmt.Errorf("read Slurm token: %w", err)
	}
	token := strings.TrimSpace(string(b))
	token = strings.TrimPrefix(token, "SLURM_JWT=")
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("Slurm token file is empty or malformed")
	}
	return token, nil
}

func gresToTRES(gres string) string {
	if gres == "" {
		return ""
	}
	parts := strings.Split(gres, ":")
	if len(parts) < 2 {
		return gres
	}
	count := parts[len(parts)-1]
	name := strings.Join(parts[:len(parts)-1], ":")
	return "gres/" + name + "=" + count
}

func unixTime(value noValue[uint64]) time.Time {
	if !value.Set || value.Infinite || value.Number == 0 {
		return time.Time{}
	}
	return time.Unix(int64(value.Number), 0).UTC()
}
