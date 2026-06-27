package planexecute

import (
	"io"

	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
)

type QueueAcquireOptions struct {
	JSONEvents bool
	Resume     string
	SessionID  string
}

type QueueSlot struct {
	Allowed bool
	State   string
	Pool    string
	Reason  string

	ReleaseFunc func()
}

func (s QueueSlot) Release() {
	if s.ReleaseFunc != nil {
		s.ReleaseFunc()
	}
}

type QueueAcquirer func(config.Config, QueueAcquireOptions, *telemetry.Recorder, io.Writer, io.Writer) QueueSlot
