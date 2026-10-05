package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const APIVersion = "slurm-gha/v1alpha1"

type Config struct {
	APIVersion string           `yaml:"apiVersion"`
	Service    ServiceConfig    `yaml:"service"`
	GitHub     GitHubConfig     `yaml:"github"`
	Slurm      SlurmConfig      `yaml:"slurm"`
	ScaleSets  []ScaleSetConfig `yaml:"scaleSets"`
}

type ServiceConfig struct {
	ListenAddress         string        `yaml:"listenAddress"`
	PublicURL             string        `yaml:"publicUrl"`
	StatePath             string        `yaml:"statePath"`
	JITEncryptionKeyFile  string        `yaml:"jitEncryptionKeyFile"`
	LogRoot               string        `yaml:"logRoot"`
	ReconcileInterval     time.Duration `yaml:"-"`
	ReconcileIntervalText string        `yaml:"reconcileInterval"`
	TLS                   TLSConfig     `yaml:"tls"`
}

type TLSConfig struct {
	CertificateFile string `yaml:"certificateFile"`
	KeyFile         string `yaml:"keyFile"`
}

type GitHubConfig struct {
	ConfigURL string          `yaml:"configUrl"`
	App       GitHubAppConfig `yaml:"app"`
}

type GitHubAppConfig struct {
	ClientID       string `yaml:"clientId"`
	InstallationID int64  `yaml:"installationId"`
	PrivateKeyFile string `yaml:"privateKeyFile"`
}

type SlurmConfig struct {
	BaseURL            string        `yaml:"baseUrl"`
	APIVersion         string        `yaml:"apiVersion"`
	User               string        `yaml:"user"`
	TokenFile          string        `yaml:"tokenFile"`
	CAFile             string        `yaml:"caFile"`
	InsecureSkipVerify bool          `yaml:"insecureSkipVerify"`
	RequestTimeout     time.Duration `yaml:"-"`
	RequestTimeoutText string        `yaml:"requestTimeout"`
}

type ScaleSetConfig struct {
	Name           string          `yaml:"name"`
	RunnerGroup    string          `yaml:"runnerGroup"`
	MaxRunners     int             `yaml:"maxRunners"`
	MaxPending     int             `yaml:"maxPending"`
	MaxQueueWait   time.Duration   `yaml:"-"`
	MaxQueueText   string          `yaml:"maxQueueWait"`
	StartupTimeout time.Duration   `yaml:"-"`
	StartupText    string          `yaml:"startupTimeout"`
	Slurm          ResourceConfig  `yaml:"slurm"`
	Execution      ExecutionConfig `yaml:"execution"`
}

type ResourceConfig struct {
	Partition   string `yaml:"partition"`
	Account     string `yaml:"account"`
	QOS         string `yaml:"qos"`
	Nodes       int    `yaml:"nodes"`
	Tasks       int    `yaml:"tasks"`
	CPUsPerTask int    `yaml:"cpusPerTask"`
	Memory      string `yaml:"memory"`
	MemoryMiB   uint64 `yaml:"-"`
	GRES        string `yaml:"gres"`
	Walltime    string `yaml:"walltime"`
	WallMinutes uint32 `yaml:"-"`
}

type ExecutionConfig struct {
	Mode        string        `yaml:"mode"`
	Image       string        `yaml:"image"`
	RunnerPath  string        `yaml:"runnerPath"`
	ScratchRoot string        `yaml:"scratchRoot"`
	MountHome   bool          `yaml:"mountHome"`
	ExtraMounts []MountConfig `yaml:"extraMounts"`
}

type MountConfig struct {
	Source      string `yaml:"source"`
	Destination string `yaml:"destination"`
	ReadOnly    bool   `yaml:"readOnly"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	if c.APIVersion != APIVersion {
		errs = append(errs, fmt.Errorf("apiVersion must be %q", APIVersion))
	}
	if c.Service.ListenAddress == "" {
		errs = append(errs, errors.New("service.listenAddress is required"))
	}
	if err := requireAbsolute("service.statePath", c.Service.StatePath); err != nil {
		errs = append(errs, err)
	}
	if err := requireAbsolute("service.logRoot", c.Service.LogRoot); err != nil {
		errs = append(errs, err)
	}
	if err := requireAbsolute("service.jitEncryptionKeyFile", c.Service.JITEncryptionKeyFile); err != nil {
		errs = append(errs, err)
	}
	if err := validateHTTPSURL("service.publicUrl", c.Service.PublicURL); err != nil {
		errs = append(errs, err)
	}
	if c.Service.ReconcileIntervalText == "" {
		c.Service.ReconcileInterval = 15 * time.Second
	} else if d, err := time.ParseDuration(c.Service.ReconcileIntervalText); err != nil || d < time.Second {
		errs = append(errs, errors.New("service.reconcileInterval must be at least one second"))
	} else {
		c.Service.ReconcileInterval = d
	}
	if (c.Service.TLS.CertificateFile == "") != (c.Service.TLS.KeyFile == "") {
		errs = append(errs, errors.New("service.tls.certificateFile and keyFile must be set together"))
	}
	if c.Service.TLS.CertificateFile == "" {
		errs = append(errs, errors.New("service TLS is required for bootstrap credential delivery"))
	}
	if err := requireAbsolute("service.tls.certificateFile", c.Service.TLS.CertificateFile); err != nil {
		errs = append(errs, err)
	}
	if err := requireAbsolute("service.tls.keyFile", c.Service.TLS.KeyFile); err != nil {
		errs = append(errs, err)
	}

	if err := validateHTTPSURL("github.configUrl", c.GitHub.ConfigURL); err != nil {
		errs = append(errs, err)
	}
	if c.GitHub.App.ClientID == "" {
		errs = append(errs, errors.New("github.app.clientId is required"))
	}
	if c.GitHub.App.InstallationID <= 0 {
		errs = append(errs, errors.New("github.app.installationId must be positive"))
	}
	if err := requireAbsolute("github.app.privateKeyFile", c.GitHub.App.PrivateKeyFile); err != nil {
		errs = append(errs, err)
	}

	if err := validateURL("slurm.baseUrl", c.Slurm.BaseURL); err != nil {
		errs = append(errs, err)
	}
	if !regexp.MustCompile(`^v0\.0\.\d+$`).MatchString(c.Slurm.APIVersion) {
		errs = append(errs, errors.New("slurm.apiVersion must look like v0.0.42"))
	}
	if c.Slurm.User == "" {
		errs = append(errs, errors.New("slurm.user is required"))
	}
	if err := requireAbsolute("slurm.tokenFile", c.Slurm.TokenFile); err != nil {
		errs = append(errs, err)
	}
	if c.Slurm.CAFile != "" {
		if err := requireAbsolute("slurm.caFile", c.Slurm.CAFile); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Slurm.InsecureSkipVerify {
		errs = append(errs, errors.New("slurm.insecureSkipVerify is not permitted; configure caFile instead"))
	}
	if c.Slurm.RequestTimeoutText == "" {
		c.Slurm.RequestTimeout = 30 * time.Second
	} else if d, err := time.ParseDuration(c.Slurm.RequestTimeoutText); err != nil || d <= 0 {
		errs = append(errs, errors.New("slurm.requestTimeout must be a positive duration"))
	} else {
		c.Slurm.RequestTimeout = d
	}

	if len(c.ScaleSets) == 0 {
		errs = append(errs, errors.New("at least one scaleSets entry is required"))
	}
	seen := make(map[string]struct{}, len(c.ScaleSets))
	for i := range c.ScaleSets {
		s := &c.ScaleSets[i]
		prefix := fmt.Sprintf("scaleSets[%d]", i)
		if s.Name == "" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`).MatchString(s.Name) {
			errs = append(errs, fmt.Errorf("%s.name is invalid", prefix))
		}
		if _, ok := seen[s.Name]; ok {
			errs = append(errs, fmt.Errorf("duplicate scale set name %q", s.Name))
		}
		seen[s.Name] = struct{}{}
		if s.RunnerGroup == "" {
			errs = append(errs, fmt.Errorf("%s.runnerGroup is required", prefix))
		}
		if s.MaxRunners < 1 {
			errs = append(errs, fmt.Errorf("%s.maxRunners must be positive", prefix))
		}
		if s.MaxPending < 1 || s.MaxPending > s.MaxRunners {
			errs = append(errs, fmt.Errorf("%s.maxPending must be between 1 and maxRunners", prefix))
		}
		if d, err := time.ParseDuration(s.MaxQueueText); err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s.maxQueueWait must be a positive duration", prefix))
		} else {
			s.MaxQueueWait = d
		}
		if d, err := time.ParseDuration(s.StartupText); err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s.startupTimeout must be a positive duration", prefix))
		} else {
			s.StartupTimeout = d
		}
		errs = append(errs, validateResources(prefix+".slurm", &s.Slurm)...)
		errs = append(errs, validateExecution(prefix+".execution", &s.Execution)...)
	}
	return errors.Join(errs...)
}

func validateResources(prefix string, r *ResourceConfig) []error {
	var errs []error
	if r.Partition == "" {
		errs = append(errs, fmt.Errorf("%s.partition is required", prefix))
	}
	if r.Account == "" {
		errs = append(errs, fmt.Errorf("%s.account is required", prefix))
	}
	if r.Nodes != 1 {
		errs = append(errs, fmt.Errorf("%s.nodes must be 1 in v1alpha1", prefix))
	}
	if r.Tasks != 1 {
		errs = append(errs, fmt.Errorf("%s.tasks must be 1 to prevent duplicate runners", prefix))
	}
	if r.CPUsPerTask < 1 {
		errs = append(errs, fmt.Errorf("%s.cpusPerTask must be positive", prefix))
	}
	if m, err := ParseMemoryMiB(r.Memory); err != nil || m == 0 {
		errs = append(errs, fmt.Errorf("%s.memory: %v", prefix, err))
	} else {
		r.MemoryMiB = m
	}
	if minutes, err := ParseWalltimeMinutes(r.Walltime); err != nil {
		errs = append(errs, fmt.Errorf("%s.walltime: %v", prefix, err))
	} else {
		r.WallMinutes = minutes
	}
	return errs
}

func validateExecution(prefix string, e *ExecutionConfig) []error {
	var errs []error
	switch e.Mode {
	case "pyxis":
		if e.Image == "" {
			errs = append(errs, fmt.Errorf("%s.image is required in pyxis mode", prefix))
		}
	case "native":
		if err := requireAbsolute(prefix+".runnerPath", e.RunnerPath); err != nil {
			errs = append(errs, err)
		}
	default:
		errs = append(errs, fmt.Errorf("%s.mode must be pyxis or native", prefix))
	}
	if err := requireAbsolute(prefix+".scratchRoot", e.ScratchRoot); err != nil {
		errs = append(errs, err)
	}
	for i, m := range e.ExtraMounts {
		if err := requireAbsolute(fmt.Sprintf("%s.extraMounts[%d].source", prefix, i), m.Source); err != nil {
			errs = append(errs, err)
		}
		if err := requireAbsolute(fmt.Sprintf("%s.extraMounts[%d].destination", prefix, i), m.Destination); err != nil {
			errs = append(errs, err)
		}
		if strings.ContainsAny(m.Source, ",:") || strings.ContainsAny(m.Destination, ",:") {
			errs = append(errs, fmt.Errorf("%s.extraMounts[%d] paths must not contain comma or colon", prefix, i))
		}
	}
	return errs
}

func ParseMemoryMiB(value string) (uint64, error) {
	m := regexp.MustCompile(`^([1-9][0-9]*)([KMGT])$`).FindStringSubmatch(strings.ToUpper(value))
	if m == nil {
		return 0, errors.New("must be an integer followed by K, M, G, or T")
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	shift := map[string]uint{"K": 0, "M": 0, "G": 10, "T": 20}[m[2]]
	if m[2] == "K" {
		n = (n + 1023) / 1024
	} else {
		n <<= shift
	}
	if n == 0 {
		return 0, errors.New("memory rounds to zero MiB")
	}
	return n, nil
}

func ParseWalltimeMinutes(value string) (uint32, error) {
	m := regexp.MustCompile(`^(?:(\d+)-)?(\d{1,2}):(\d{2}):(\d{2})$`).FindStringSubmatch(value)
	if m == nil {
		return 0, errors.New("must be [days-]HH:MM:SS")
	}
	days, _ := strconv.ParseUint(m[1], 10, 32)
	hours, _ := strconv.ParseUint(m[2], 10, 32)
	mins, _ := strconv.ParseUint(m[3], 10, 32)
	secs, _ := strconv.ParseUint(m[4], 10, 32)
	if hours > 23 && m[1] != "" || mins > 59 || secs > 59 {
		return 0, errors.New("walltime components are out of range")
	}
	totalSeconds := days*86400 + hours*3600 + mins*60 + secs
	if totalSeconds == 0 {
		return 0, errors.New("must be greater than zero")
	}
	minutes := (totalSeconds + 59) / 60
	if minutes > 1<<32-1 {
		return 0, errors.New("walltime is too large")
	}
	return uint32(minutes), nil
}

func requireAbsolute(name, value string) error {
	if value == "" || !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute path", name)
	}
	return nil
}

func validateURL(name, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	return nil
}

func validateHTTPSURL(name, value string) error {
	if err := validateURL(name, value); err != nil {
		return err
	}
	u, _ := url.Parse(value)
	if u.Scheme != "https" {
		return fmt.Errorf("%s must use https", name)
	}
	return nil
}
