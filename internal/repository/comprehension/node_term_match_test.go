package comprehension

import "testing"

func TestNodeTermMatchScoreNamespaceQualifiedExactName(t *testing.T) {
	exact := Node{Kind: "type", Path: "client/ambience.h", QualifiedName: "bt::client::Ambience",
		Properties: map[string]any{"name": "Ambience"}}
	nsOnly := Node{Kind: "type", Path: "client/wind_clipmap.h", QualifiedName: "bt::client::WindClipmap",
		Properties: map[string]any{"name": "WindClipmap"}}

	if got := nodeTermMatchScore(exact, "ambience"); got < 80 {
		t.Fatalf("qualified node with bare name == term scored %d, want >= 80", got)
	}

	if e, n := nodeTermMatchScore(exact, "ambience"), nodeTermMatchScore(nsOnly, "client"); e <= n {
		t.Fatalf("exact bare-name match %d must outrank namespace-token-only match %d", e, n)
	}
}
