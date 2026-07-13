package workspace

import (
	"context"
	"strings"
)

type executionSessionIDContextKey struct{}

type executionSessionIdentity struct {
	sessionID string
	explicit  bool
}

type executionSessionGrantKey struct {
	sessionID     string
	explicitEmpty bool
}

func (i executionSessionIdentity) grantKey() executionSessionGrantKey {
	return executionSessionGrantKey{
		sessionID:     i.sessionID,
		explicitEmpty: i.explicit && i.sessionID == "",
	}
}

// WithExecutionSessionID binds workspace side effects to the session that
// owns one invocation. The identity is immutable for the lifetime of the
// returned context, including tool work that outlives its caller's timeout.
func WithExecutionSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, executionSessionIDContextKey{}, executionSessionIdentity{
		sessionID: strings.TrimSpace(sessionID),
		explicit:  true,
	})
}

// ExecutionSessionIDFromContext reports the execution-session identity bound
// to ctx. An explicitly empty identity is still bound and returns ("", true).
func ExecutionSessionIDFromContext(ctx context.Context) (sessionID string, bound bool) {
	identity, ok := executionSessionIdentityFromContext(ctx)
	if !ok {
		return "", false
	}

	return identity.sessionID, true
}

func executionSessionIdentityFromContext(ctx context.Context) (executionSessionIdentity, bool) {
	if ctx == nil {
		return executionSessionIdentity{}, false
	}

	identity, ok := ctx.Value(executionSessionIDContextKey{}).(executionSessionIdentity)
	if !ok {
		return executionSessionIdentity{}, false
	}

	return identity, true
}

func (m *Manager) snapshotExecutionSession(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := executionSessionIdentityFromContext(ctx); ok {
		return ctx
	}

	return context.WithValue(ctx, executionSessionIDContextKey{}, executionSessionIdentity{
		sessionID: m.SessionID(),
	})
}
