package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/sessionlog"
)

// Options is the UI-independent configuration for a headless prompt session.
// Delivery adapters may carry additional concerns such as telemetry, but turn
// behavior and session initialization belong here.
type Options struct {
	Resume                      string
	JSONEvents                  bool
	TaskFamily                  string
	PlannerOwnedVerification    bool
	MaxTokensOverride           int
	DisableThinking             bool
	ReviewRepairOpenObligations []string
	UseActiveModel              bool
	ScopeContract               plancontract.PlanScopeContract
	AuthorizedNewFiles          []string
	OnSourceSatisfied           func([]string)
}

//go:generate go tool mockgen -source=session.go -destination=sessionmocks/mock_engine.go -package=sessionmocks -typed

// Engine is the prompt-capable runtime consumed by a headless session. The
// composition root supplies the concrete engine; CLI lifecycle code depends
// only on this runtime contract.
type Engine interface {
	ConfigureHeadlessSession(Options)
	DebugLogPath() string
	LoadHistoryFromSession(string) error
	SetJSONEventStream(io.Writer, string)
	RunPromptOnce(context.Context, string) (string, error)
	Close() error
}

type Factory func(config.Config) Engine

// ErrSessionClosed reports that Close has won turn admission permanently.
var ErrSessionClosed = errors.New("prompt session is closed")

type activeTurn struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Session owns headless option application, resume/event initialization,
// per-turn timeouts, and closeout. One Session may execute multiple prompts so
// turns-file mode retains a single conversation and runtime instance.
type Session struct {
	cfg          config.Config
	engine       Engine
	debugLogPath string
	turnGate     sync.Mutex
	lifecycleMu  sync.Mutex
	closed       bool
	activeTurn   *activeTurn
	closeOnce    sync.Once
	closeErr     error
}

func Open(cfg config.Config, factory Factory, opts Options, eventWriter io.Writer) (*Session, error) {
	if factory == nil {
		return nil, fmt.Errorf("prompt session factory is required")
	}

	engine := factory(cfg)
	if engine == nil {
		return nil, fmt.Errorf("prompt session factory returned no engine")
	}

	s := &Session{cfg: cfg, engine: engine}
	engine.ConfigureHeadlessSession(opts)

	if opts.Resume != "" {
		path := sessionlog.ResolveSessionLogPath(cfg, opts.Resume)
		if err := engine.LoadHistoryFromSession(path); err != nil {
			closeErr := s.Close()

			return nil, errors.Join(fmt.Errorf("resume %q: %w", opts.Resume, err), closeErr)
		}
	}
	s.debugLogPath = engine.DebugLogPath()

	if opts.JSONEvents {
		sessionID := ""
		if path := s.debugLogPath; path != "" {
			sessionID = sessionlog.SessionIDFromPath(path)
		}
		engine.SetJSONEventStream(eventWriter, sessionID)
	}

	return s, nil
}

func (s *Session) RunPrompt(ctx context.Context, prompt string) (string, error) {
	if s == nil || s.engine == nil {
		return "", fmt.Errorf("prompt session is not initialized")
	}

	s.turnGate.Lock()
	defer s.turnGate.Unlock()

	timeout := time.Duration(s.cfg.Chat.RequestTimeoutSeconds+s.cfg.Chat.ServerStartTimeoutSecs) * time.Second
	var turnCtx context.Context
	var cancel context.CancelFunc
	if timeout <= 0 {
		turnCtx, cancel = context.WithCancel(ctx)
	} else {
		turnCtx, cancel = context.WithTimeout(ctx, timeout)
	}

	turn := &activeTurn{cancel: cancel, done: make(chan struct{})}
	s.lifecycleMu.Lock()
	if s.closed {
		s.lifecycleMu.Unlock()
		cancel()

		return "", ErrSessionClosed
	}
	s.activeTurn = turn
	s.lifecycleMu.Unlock()
	defer func() {
		cancel()
		s.lifecycleMu.Lock()
		if s.activeTurn == turn {
			s.activeTurn = nil
		}
		close(turn.done)
		s.lifecycleMu.Unlock()
	}()

	return s.engine.RunPromptOnce(turnCtx, prompt)
}

func (s *Session) RunPrompts(ctx context.Context, prompts []string) ([]string, error) {
	answers := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		answer, err := s.RunPrompt(ctx, prompt)
		if err != nil {
			return answers, err
		}
		answers = append(answers, answer)
	}

	return answers, nil
}

func (s *Session) DebugLogPath() string {
	if s == nil {
		return ""
	}

	return s.debugLogPath
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}

	s.closeOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.closed = true
		turn := s.activeTurn
		s.lifecycleMu.Unlock()
		if turn != nil {
			turn.cancel()
			<-turn.done
		}

		if s.engine != nil {
			if err := s.engine.Close(); err != nil {
				s.closeErr = fmt.Errorf("close prompt session engine: %w", err)
			}
		}
	})

	return s.closeErr
}
