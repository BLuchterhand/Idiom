package cpp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	. "github.com/onsi/ginkgo/v2"
)

// Cancel at a progress boundary, without a timing-sensitive race against the
// parser or assumptions about the machine's speed.
type progressCancelContext struct {
	context.Context
	cancel context.CancelFunc
	polls  int
	mu     sync.Mutex
}

func (c *progressCancelContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.polls--
	if c.polls <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}

var _ = It("C++ cache retains parsed files after cancellation", func() {
	t := GinkgoT()
	root := t.TempDir()
	for i := 0; i < 15; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%02d.cpp", i)), []byte(fmt.Sprintf("void f%d() {}\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	partial, err := BuildCppIndexWithOptions(root, BuildOptions{Context: &progressCancelContext{Context: ctx, cancel: cancel, polls: 22}})
	var budget *BudgetError
	if !errors.As(err, &budget) || partial == nil || !partial.WalkComplete || len(partial.Files) == 0 || len(partial.Files) == 15 {
		t.Fatalf("expected retained partial parse: %+v %v", partial, err)
	}
	resumed, err := BuildCppIndexWithOptions(root, BuildOptions{Previous: partial, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Timings.Reused != len(partial.Files) || resumed.Timings.Parsed+resumed.Timings.Reused != 15 {
		t.Fatalf("resume: %+v", resumed.Timings)
	}
	full, err := BuildCppIndex(root)
	if err != nil || !reflect.DeepEqual(full.Files, resumed.Files) {
		t.Fatalf("resume differs from full build: %v", err)
	}
	// A canceled walk of a different snapshot must drop the changed old file.
	partial, err = BuildCppIndexWithOptions(root, BuildOptions{Context: ctx, Previous: full, ChangedPaths: map[string]bool{"file00.cpp": true}})
	if !errors.As(err, &budget) || len(partial.Files) != 14 {
		t.Fatalf("walk retained stale changed file: %+v %v", partial, err)
	}
})
