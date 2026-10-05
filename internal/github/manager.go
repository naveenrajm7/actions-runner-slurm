package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/naveenrajm7/actions-runner-slurm/internal/config"
	"github.com/naveenrajm7/actions-runner-slurm/internal/reconcile"
)

type scaleSetRegistration struct {
	class config.ScaleSetConfig
	id    int
}

type runningListener struct {
	class   string
	session *scaleset.MessageSessionClient
	listen  *listener.Listener
}

type Manager struct {
	client        *scaleset.Client
	registrations map[string]scaleSetRegistration
	logger        *slog.Logger
	owner         string
}

func NewManager(cfg config.GitHubConfig, logger *slog.Logger) (*Manager, error) {
	keyInfo, err := os.Stat(cfg.App.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("stat GitHub App private key: %w", err)
	}
	if keyInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("GitHub App private key must not be group- or world-accessible")
	}
	privateKey, err := os.ReadFile(cfg.App.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read GitHub App private key: %w", err)
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	client, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: cfg.ConfigURL,
		GitHubAppAuth: scaleset.GitHubAppAuth{
			ClientID: cfg.App.ClientID, InstallationID: cfg.App.InstallationID, PrivateKey: string(privateKey),
		},
		SystemInfo: scaleset.SystemInfo{System: "actions-runner-slurm", Version: "dev", Subsystem: "service"},
	})
	if err != nil {
		return nil, fmt.Errorf("create GitHub scale-set client: %w", err)
	}
	owner, err := os.Hostname()
	if err != nil || owner == "" {
		owner = "actions-runner-slurm"
	}
	return &Manager{client: client, registrations: make(map[string]scaleSetRegistration), logger: logger, owner: owner}, nil
}

func (m *Manager) EnsureScaleSets(ctx context.Context, classes []config.ScaleSetConfig) error {
	for _, class := range classes {
		groupID := 1
		if class.RunnerGroup != scaleset.DefaultRunnerGroup {
			group, err := m.client.GetRunnerGroupByName(ctx, class.RunnerGroup)
			if err != nil {
				return fmt.Errorf("resolve runner group %q for class %q: %w", class.RunnerGroup, class.Name, err)
			}
			groupID = group.ID
		}
		registered, err := m.client.GetRunnerScaleSet(ctx, groupID, class.Name)
		if err != nil {
			return fmt.Errorf("look up runner scale set %q: %w", class.Name, err)
		}
		if registered == nil {
			registered, err = m.client.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
				Name: class.Name, RunnerGroupID: groupID,
				Labels:        []scaleset.Label{{Name: class.Name, Type: "System"}},
				RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
			})
			if err != nil {
				return fmt.Errorf("create runner scale set %q: %w", class.Name, err)
			}
			m.logger.Info("created GitHub runner scale set", "class", class.Name, "scale_set_id", registered.ID)
		} else {
			m.logger.Info("using existing GitHub runner scale set", "class", class.Name, "scale_set_id", registered.ID)
		}
		m.registrations[class.Name] = scaleSetRegistration{class: class, id: registered.ID}
	}
	return nil
}

func (m *Manager) GenerateJIT(ctx context.Context, className, runnerName string) (string, error) {
	registration, ok := m.registrations[className]
	if !ok {
		return "", fmt.Errorf("scale set %q is not registered", className)
	}
	jit, err := m.client.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{
		Name: runnerName, WorkFolder: "_work",
	}, registration.id)
	if err != nil {
		return "", fmt.Errorf("generate JIT for class %q: %w", className, err)
	}
	if jit == nil || jit.EncodedJITConfig == "" {
		return "", errors.New("GitHub returned an empty JIT configuration")
	}
	return jit.EncodedJITConfig, nil
}

func (m *Manager) RunListeners(ctx context.Context, controller *reconcile.Controller) error {
	if len(m.registrations) == 0 {
		return errors.New("runner scale sets have not been registered")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	running := make([]runningListener, 0, len(m.registrations))
	for className, registration := range m.registrations {
		session, err := m.client.MessageSessionClient(runCtx, registration.id, listenerOwner(m.owner, className))
		if err != nil {
			closeSessions(running)
			return fmt.Errorf("create message session for %q: %w", className, err)
		}
		listen, err := listener.New(session, listener.Config{
			ScaleSetID: registration.id, MaxRunners: registration.class.MaxRunners,
			Logger: m.logger.With("class", className).WithGroup("listener"),
		})
		if err != nil {
			_ = session.Close(context.Background())
			closeSessions(running)
			return err
		}
		running = append(running, runningListener{class: className, session: session, listen: listen})
	}
	defer closeSessions(running)
	errCh := make(chan error, len(running))
	var wg sync.WaitGroup
	for _, item := range running {
		wg.Add(1)
		go func(item runningListener) {
			defer wg.Done()
			scaler := &scalerAdapter{className: item.class, controller: controller, logger: m.logger.With("class", item.class)}
			if err := item.listen.Run(runCtx, scaler); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("listener %q: %w", item.class, err)
			}
		}(item)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	case err := <-errCh:
		cancel()
		<-done
		return err
	case <-done:
		return nil
	}
}

func closeSessions(running []runningListener) {
	for _, item := range running {
		_ = item.session.Close(context.Background())
	}
}

type scalerAdapter struct {
	className  string
	controller *reconcile.Controller
	logger     *slog.Logger
}

func (s *scalerAdapter) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	return s.controller.SetDesired(ctx, s.className, count)
}

func (s *scalerAdapter) HandleJobStarted(_ context.Context, job *scaleset.JobStarted) error {
	if job == nil || job.RunnerName == "" {
		return nil
	}
	if err := s.controller.JobStarted(job.RunnerName, int64(job.RunnerID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("job started for an unknown runner", "runner_name", job.RunnerName)
			return nil
		}
		return err
	}
	return nil
}

func (s *scalerAdapter) HandleJobCompleted(_ context.Context, job *scaleset.JobCompleted) error {
	if job == nil || job.RunnerName == "" {
		return nil
	}
	if err := s.controller.JobCompleted(job.RunnerName, job.Result); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("job completed for an unknown runner; no allocation was canceled", "runner_name", job.RunnerName)
			return nil
		}
		return err
	}
	return nil
}

func listenerOwner(hostname, className string) string {
	value := hostname + "-" + className
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, value)
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}
