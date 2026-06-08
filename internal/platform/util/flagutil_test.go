package util

import (
	"flag"
	"testing"
)

func TestMultiFlag(t *testing.T) {
	var mf MultiFlag
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Var(&mf, "f", "test flag")
	fs.Parse([]string{"-f", "a", "-f", "b", "-f", "c"})
	if len(mf) != 3 {
		t.Fatalf("expected 3 values, got %d: %v", len(mf), mf)
	}
	if mf.String() != "a,b,c" {
		t.Fatalf("expected a,b,c got %s", mf.String())
	}
}
