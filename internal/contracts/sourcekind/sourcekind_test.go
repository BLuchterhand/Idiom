package sourcekind

import "testing"

func TestCoversSourceSanity(t *testing.T) {
	for _, path := range []string{
		"main.go",
		"native/sim.cpp",
		"types/index.d.ts",
		"scripts/demo.gd",
		"src/app.tsx",
	} {
		if !CoversSourceSanity(path) {
			t.Fatalf("CoversSourceSanity(%q) = false", path)
		}
	}

	for _, path := range []string{"README.md", "config.yaml", ""} {
		if CoversSourceSanity(path) {
			t.Fatalf("CoversSourceSanity(%q) = true", path)
		}
	}
}

func TestRequiresConcreteSourceSanityDiagnostics(t *testing.T) {
	if !RequiresConcreteSourceSanityDiagnostics("native/sim.hpp") {
		t.Fatal("C++ path should require concrete diagnostics")
	}
	if RequiresConcreteSourceSanityDiagnostics("main.go") {
		t.Fatal("Go path should not require concrete source_sanity diagnostics")
	}
}
