package cpp

import "fmt"

// BudgetError reports that an index build was cancelled or ran out of its
// context deadline, naming the phase that was running and the time spent so far.
type BudgetError struct {
	Phase   string
	Timings CppBuildTimings
	Cause   error
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("C++ index build stopped during %s phase (walk %dms, parse %dms, %d files read): %v",
		e.Phase, e.Timings.Walk.Milliseconds(), e.Timings.Parse.Milliseconds(), e.Timings.Files, e.Cause)
}

func (e *BudgetError) Unwrap() error { return e.Cause }
