package semantic

import "context"

type IndexOptions struct {
	Scope    string
	MaxFiles int
	Context  context.Context
	// WorkerCount overrides the measured parser parallelism for benchmarks and
	// deterministic performance comparisons. A non-positive value selects the
	// production measurement-derived count.
	WorkerCount int
}
