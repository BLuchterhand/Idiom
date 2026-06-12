package patching

import "testing"

func TestParseAddUpdateDeleteMove(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Add File: new/hello.txt\n" +
		"+hello\n" +
		"+world\n" +
		"*** Update File: existing.go\n" +
		"*** Move to: renamed.go\n" +
		"@@\n" +
		" package main\n" +
		"-old line\n" +
		"+new line\n" +
		"*** Delete File: gone.txt\n" +
		"*** End Patch"

	ops, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}

	if ops[0].Kind != OpAdd || ops[0].Path != "new/hello.txt" || ops[0].NewBody != "hello\nworld" {
		t.Fatalf("unexpected add op: %#v", ops[0])
	}

	if ops[1].Kind != OpUpdate || ops[1].Path != "existing.go" || ops[1].MoveTo != "renamed.go" {
		t.Fatalf("unexpected update op: %#v", ops[1])
	}

	if len(ops[1].Hunks) != 1 || len(ops[1].Hunks[0].Lines) != 3 {
		t.Fatalf("unexpected hunks: %#v", ops[1].Hunks)
	}

	if ops[2].Kind != OpDelete || ops[2].Path != "gone.txt" {
		t.Fatalf("unexpected delete op: %#v", ops[2])
	}
}

func TestParseRequiresEnvelope(t *testing.T) {
	if _, err := Parse("*** Add File: x\n+y\n"); err == nil {
		t.Fatal("expected envelope error")
	}

	if _, err := Parse("*** Begin Patch\n+orphan\n*** End Patch"); err == nil {
		t.Fatal("expected unexpected-line error")
	}

	if _, err := Parse("*** Begin Patch\n*** Add File: f\nbad-prefix\n*** End Patch"); err == nil {
		t.Fatal("expected add-line prefix error")
	}

	if _, err := Parse("*** Begin Patch\n*** Update File: f\nbad-line\n*** End Patch"); err == nil {
		t.Fatal("expected update-without-hunk error")
	}

	if _, err := Parse("*** Begin Patch\n*** Move to: x\n*** End Patch"); err == nil {
		t.Fatal("expected move-without-update error")
	}

	if _, err := Parse("*** Begin Patch\n*** End Patch"); err == nil {
		t.Fatal("expected empty-patch error")
	}
}

func TestSerializeRoundTripsAddUpdateDelete(t *testing.T) {
	original := "*** Begin Patch\n" +
		"*** Add File: created/note.md\n" +
		"+# Note\n" +
		"+body\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n" +
		"*** Delete File: doomed.txt\n" +
		"*** End Patch\n"

	ops, err := Parse(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	encoded := Serialize(ops)

	round, err := Parse(encoded)
	if err != nil {
		t.Fatalf("re-parse: %v\n--- encoded ---\n%s", err, encoded)
	}

	if len(round) != len(ops) {
		t.Fatalf("op count differs after round-trip: got %d want %d", len(round), len(ops))
	}

	for i := range ops {
		if round[i].Kind != ops[i].Kind {
			t.Fatalf("op %d kind: got %q want %q", i, round[i].Kind, ops[i].Kind)
		}

		if round[i].Path != ops[i].Path {
			t.Fatalf("op %d path: got %q want %q", i, round[i].Path, ops[i].Path)
		}

		if round[i].NewBody != ops[i].NewBody {
			t.Fatalf("op %d body: got %q want %q", i, round[i].NewBody, ops[i].NewBody)
		}

		if len(round[i].Hunks) != len(ops[i].Hunks) {
			t.Fatalf("op %d hunk count: got %d want %d", i, len(round[i].Hunks), len(ops[i].Hunks))
		}
	}
}

func TestParseTolerantOfUnknownTripleStarDirectives(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"*** Sanity Check\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"-beta\n" +
		"+BETA\n" +
		" gamma\n" +
		"*** Note: this is a model commentary directive\n" +
		"*** Add File: new.txt\n" +
		"+content\n" +
		"*** Verification\n" +
		"*** End Patch"

	ops, err := Parse(patch)
	if err != nil {
		t.Fatalf("expected unknown *** directives to be tolerated, got: %v", err)
	}

	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (update, add), got %d: %#v", len(ops), ops)
	}

	if ops[0].Kind != OpUpdate || ops[0].Path != "existing.txt" {
		t.Fatalf("first op should update existing.txt, got %#v", ops[0])
	}

	if ops[1].Kind != OpAdd || ops[1].Path != "new.txt" {
		t.Fatalf("second op should add new.txt, got %#v", ops[1])
	}
}

func TestParseStillRejectsBareTextOutsideFileSection(t *testing.T) {
	patch := "*** Begin Patch\n" +
		"this is just prose, not a directive\n" +
		"*** Update File: existing.txt\n" +
		"@@\n" +
		" alpha\n" +
		"+beta\n" +
		"*** End Patch"
	if _, err := Parse(patch); err == nil {
		t.Fatal("expected bare prose between sections to fail parsing")
	}
}

func TestOpPaths(t *testing.T) {
	ops := []Op{
		{Kind: OpUpdate, Path: "a.go", MoveTo: "b.go"},
		{Kind: OpAdd, Path: "c.go"},
		{Kind: OpDelete, Path: "a.go"},
	}
	got := OpPaths(ops)
	want := []string{"a.go", "b.go", "c.go"}

	if len(got) != len(want) {
		t.Fatalf("OpPaths len=%d, want %d (%v)", len(got), len(want), got)
	}

	for i, p := range want {
		if got[i] != p {
			t.Fatalf("OpPaths[%d] = %q, want %q", i, got[i], p)
		}
	}
}
