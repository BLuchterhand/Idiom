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
	tests := []struct {
		path string
		want bool
	}{
		{path: "native/sim.c", want: true},
		{path: "native/sim.cc", want: true},
		{path: "native/sim.cpp", want: true},
		{path: "native/sim.cxx", want: true},
		{path: "native/sim.h", want: true},
		{path: "native/sim.hh", want: true},
		{path: "native/sim.hpp", want: true},
		{path: "native/sim.hxx", want: true},
		{path: "native/sim.ipp", want: true},
		{path: "native/sim.inl", want: true},
		{path: "native/sim.tpp", want: true},
		{path: "  NATIVE/SIM.CPP  ", want: true},
		{path: "main.go", want: false},
		{path: "src/app.ts", want: false},
		{path: "src/lib.rs", want: false},
		{path: "", want: false},
	}

	for _, tc := range tests {
		if got := RequiresConcreteSourceSanityDiagnostics(tc.path); got != tc.want {
			t.Fatalf("RequiresConcreteSourceSanityDiagnostics(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
