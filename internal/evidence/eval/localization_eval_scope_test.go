package eval

import "testing"

func TestCommonGoldScope(t *testing.T) {
	for _, c := range []struct {
		files []string
		depth int
		want  string
	}{
		{[]string{"client/a.cpp", "client/ui/b.cpp"}, 2, "client"},
		{[]string{"core/worldgen/a.cpp", "core/worldgen/x/b.cpp"}, 2, "core/worldgen"},
		{[]string{"core/worldgen/a/b/c.cpp", "core/worldgen/a/b/d.cpp"}, 2, "core/worldgen"},
		{[]string{"core/a.cpp", "client/b.cpp"}, 2, ""},
		{[]string{"README.go", "client/b.cpp"}, 2, ""},
	} {
		if got := CommonGoldScope(c.files, c.depth); got != c.want {
			t.Errorf("CommonGoldScope(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}
