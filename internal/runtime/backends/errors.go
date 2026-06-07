package backends

import "idiom/internal/platform/failures"

const (
	reasonBackendLsofUnavailable failures.Reason = 10001 + iota
	reasonBackendProcUnavailable
)
