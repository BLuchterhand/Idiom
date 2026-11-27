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

// cacheTestFiles is more than cppMinParsedPerCall, so a cancelled build stops partway.
const cacheTestFiles = 80

var _ = It("C++ cache retains parsed files after cancellation", func() {
	t := GinkgoT()
	root := t.TempDir()
	for i := 0; i < cacheTestFiles; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%02d.cpp", i)), []byte(fmt.Sprintf("void f%d() {}\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	partial, err := BuildCppIndexWithOptions(root, BuildOptions{Context: &progressCancelContext{Context: ctx, cancel: cancel, polls: 22}})
	var budget *BudgetError
	if !errors.As(err, &budget) || partial == nil || !partial.WalkComplete || len(partial.Files) != cppMinParsedPerCall {
		t.Fatalf("expected retained partial parse: %+v %v", partial, err)
	}
	resumed, err := BuildCppIndexWithOptions(root, BuildOptions{Previous: partial, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Timings.Reused != len(partial.Files) || resumed.Timings.Parsed+resumed.Timings.Reused != cacheTestFiles {
		t.Fatalf("resume: %+v", resumed.Timings)
	}
	full, err := BuildCppIndex(root)
	if err != nil || !reflect.DeepEqual(full.Files, resumed.Files) {
		t.Fatalf("resume differs from full build: %v", err)
	}
	// A stopped incremental build must not keep the changed file's old entry.
	if err := os.WriteFile(filepath.Join(root, "file00.cpp"), []byte("void changed() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := BuildCppIndexWithOptions(root, BuildOptions{Context: ctx, Previous: full, ChangedPaths: map[string]bool{"file00.cpp": true}})
	if err != nil || next.Timings.Parsed != 1 || next.FileByPath("file00.cpp").Symbols[0].Name != "changed" {
		t.Fatalf("stale changed file: %+v %v", next, err)
	}
})
