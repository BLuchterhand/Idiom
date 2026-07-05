package plan

import (
	"strings"
	"testing"
)

func TestRequiredEditSnippetsFromPromptSkipsCurrentStateAndBindsSinglePath(t *testing.T) {
	prompt := `
# IMPLEMENTATION REQUEST

## Verified Current State

` + "```cpp" + `
TEST_CASE("old behavior") {}
` + "```" + `

## Required Edits

Add the logistics include:
` + "```cpp" + `
#include "core/constants/logistics.h"
` + "```" + `

Insert the demand-pressure regression:
` + "```cpp" + `
TEST_CASE("demand pressure caps production") {
	CHECK(DEMAND_PRESSURE_CAP > 0.0);
}
` + "```" + `
`
	snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/tests/test_production_graph.cpp"})

	if got := len(snippets); got != 2 {
		t.Fatalf("expected two required edit snippets, got %d: %#v", got, snippets)
	}
	for i, snippet := range snippets {
		if snippet.ID == "" {
			t.Fatalf("snippet %d missing id: %#v", i, snippet)
		}
		if snippet.Path != "native/tests/test_production_graph.cpp" {
			t.Fatalf("snippet %d path = %q, want test file", i, snippet.Path)
		}
		if strings.Contains(snippet.Content, "old behavior") {
			t.Fatalf("verified-current-state snippet should not be captured: %#v", snippet)
		}
	}
	if !strings.Contains(snippets[0].Content, `#include "core/constants/logistics.h"`) {
		t.Fatalf("missing include snippet: %#v", snippets)
	}
	if !strings.Contains(snippets[1].Content, "demand pressure caps production") {
		t.Fatalf("missing regression snippet: %#v", snippets)
	}
}

func TestRequiredEditSnippetsFromPromptAssignsMultiFilePaths(t *testing.T) {
	prompt := `
## Required Edits

In ` + "`native/src/a.cpp`" + `, replace the source line.

old_text:

` + "```text" + `
int before();
` + "```" + `

new_text:

` + "```text" + `
int after();
` + "```" + `

In ` + "`native/tests/test_a.cpp`" + `, replace the test case.

old_text:

` + "```text" + `
TEST_CASE("before") {}
` + "```" + `

new_text:

` + "```text" + `
TEST_CASE("after") {}
` + "```" + `
`
	snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.cpp", "native/tests/test_a.cpp"})

	if got := len(snippets); got != 2 {
		t.Fatalf("expected two new snippets, got %d: %#v", got, snippets)
	}

	got := map[string]string{}
	for _, snippet := range snippets {
		got[snippet.Path] = snippet.Content
	}
	if !strings.Contains(got["native/src/a.cpp"], "int after();") {
		t.Fatalf("source snippet path/content not captured correctly: %#v", snippets)
	}
	if !strings.Contains(got["native/tests/test_a.cpp"], `TEST_CASE("after")`) {
		t.Fatalf("test snippet path/content not captured correctly: %#v", snippets)
	}
}

func TestRequiredEditSnippetsFromPromptDuplicatesSharedBasenameSnippetPaths(t *testing.T) {
	prompt := `
## Required Edits

Update the ` + "`a.h`" + ` and ` + "`a.cpp`" + ` comments.

old_text:

` + "```text" + `
// old shared comment
` + "```" + `

new_text:

` + "```text" + `
// new shared comment
` + "```" + `
`
	snippets := RequiredEditSnippetsFromPrompt(prompt, []string{"native/src/a.h", "native/src/a.cpp"})

	got := map[string]string{}
	for _, snippet := range snippets {
		got[snippet.Path] = snippet.Content
	}
	for _, path := range []string{"native/src/a.h", "native/src/a.cpp"} {
		if !strings.Contains(got[path], "// new shared comment") {
			t.Fatalf("shared snippet was not duplicated for %s: %#v", path, snippets)
		}
	}
}
