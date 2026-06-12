package queue

import (
	"strings"
	"testing"
)

func TestFormatJobAgeRendersUnitsCorrectly(t *testing.T) {
	cases := []struct {
		seconds int
		idle    bool
		want    string
	}{
		{0, false, "0s"},
		{5, false, "5s"},
		{59, false, "59s"},
		{60, false, "1m0s"},
		{125, false, "2m5s"},
		{3599, false, "59m59s"},
		{3600, false, "1h0m"},
		{7325, false, "2h2m"},
		{-1, false, "0s"},
	}
	for _, c := range cases {
		got := formatJobAge(c.seconds, c.idle)
		if got != c.want {
			t.Errorf("formatJobAge(%d, %v) = %q, want %q", c.seconds, c.idle, got, c.want)
		}
	}
}

func TestFormatJobAgeAppendsIdleSuffix(t *testing.T) {
	got := formatJobAge(900, true)
	if !strings.HasSuffix(got, " IDLE") {
		t.Fatalf("idle suffix missing: got %q", got)
	}

	if !strings.HasPrefix(got, "15m") {
		t.Fatalf("expected 15m prefix, got %q", got)
	}
}

func TestFormatJobAgeOmitsIdleSuffixWhenFalse(t *testing.T) {
	got := formatJobAge(900, false)
	if strings.Contains(got, "IDLE") {
		t.Fatalf("idle suffix should be absent: got %q", got)
	}
}
