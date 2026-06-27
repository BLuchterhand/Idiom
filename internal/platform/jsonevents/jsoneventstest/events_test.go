package jsoneventstest

import "testing"

func TestDecodeLinesAndFindByType(t *testing.T) {
	events := DecodeLines(t, "noise\n{\"type\":\"idiom.test.one\",\"value\":1}\n{\"type\":\"idiom.plan_execute.executor_start\"}\n")

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if _, ok := FindByType(events, "idiom.test.one"); !ok {
		t.Fatal("FindByType did not find literal event")
	}
	if _, ok := FindByType(events, "idiom.test.missing"); ok {
		t.Fatal("FindByType found missing event")
	}
}
