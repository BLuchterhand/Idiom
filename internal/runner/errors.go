package runner

import (
	"strings"

	"idiom/internal/failures"
)

const (
	reasonDaemonUnreachable failures.Reason = 6001 + iota
	reasonDaemonResponseError
	reasonDaemonProtocolError
	reasonQueueConnectionClosed
	reasonUnknownPool
)

// ErrDaemonUnreachable is returned when the queue daemon cannot be reached.
var ErrDaemonUnreachable = failures.New(failures.Unavailable, reasonDaemonUnreachable, "runner daemon unreachable")

func daemonResponseError(message string) error {
	code := failures.FailedPrecondition
	reason := reasonDaemonResponseError
	if strings.Contains(strings.ToLower(strings.TrimSpace(message)), "unknown pool") {
		code = failures.InvalidArgument
		reason = reasonUnknownPool
	}
	return failures.New(code, reason, message)
}

func daemonProtocolError(message string) error {
	return failures.New(failures.Internal, reasonDaemonProtocolError, message)
}

func queueConnectionClosedError() error {
	return failures.New(failures.Unavailable, reasonQueueConnectionClosed, "queue connection closed before grant")
}
