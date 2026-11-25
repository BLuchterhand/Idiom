package eval

import "testing"

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
