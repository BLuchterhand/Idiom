package workspace

import "errors"

// Sentinel errors for workspace operations.
var (
	// ErrSourceSanityFailed is returned when source sanity checks
	// detect issues in code files.
	ErrSourceSanityFailed = errors.New("source sanity check failed")

	// ErrPlanSubmissionRejected is returned when submit_plan validation
	// or acceptance fails.
	ErrPlanSubmissionRejected = errors.New("plan submission rejected")

	// ErrPythonLSPUnavailable is returned when the Python language server
	// binary cannot be found or started.
	ErrPythonLSPUnavailable = errors.New("python language server unavailable")
)

