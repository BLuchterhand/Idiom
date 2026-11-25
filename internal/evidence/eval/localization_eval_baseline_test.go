package eval

import (
	"slices"
	"testing"
)

func TestBaselineTokensSplitsIdentifiers(t *testing.T) {
	got := BaselineTokens("SunShadow day_clock HTTPServer G713 the shadows")
	want := []string{"sun", "shadow", "sunshadow", "day", "clock", "dayclock", "http", "server", "httpserver", "g713", "shadow"}

	if !slices.Equal(got, want) {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestBM25PrefersRareTermMatch(t *testing.T) {
	query := []string{"slot", "queue"}
	set := map[string]struct{}{"slot": {}, "queue": {}}
	f := newBM25Field(3)
	f.add(0, []string{"slot", "admission"}, set)
	f.add(1, []string{"queue", "queue", "other"}, set)
	f.add(2, []string{"queue", "unrelated"}, set)

	s := f.scores(query)
	if !(s[0] > s[1] && s[1] > s[2] && s[2] > 0) {
		t.Fatalf("scores = %v", s)
	}
}
