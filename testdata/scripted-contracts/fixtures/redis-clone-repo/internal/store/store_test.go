package store

import (
	"testing"
	"time"
)

func TestStringSetGetDel(t *testing.T) {
	s := New(nil)
	s.Set("name", "idiom", 0)
	if got, ok := s.Get("name"); !ok || got != "idiom" {
		t.Fatalf("Get(name) = %q,%v", got, ok)
	}
	if removed := s.Del("name", "missing"); removed != 1 {
		t.Fatalf("Del() = %d, want 1", removed)
	}
}

func TestIncrRejectsNonInteger(t *testing.T) {
	s := New(nil)
	s.Set("count", "nope", 0)
	if _, err := s.Incr("count"); err == nil {
		t.Fatal("Incr() error = nil, want non-integer error")
	}
}

func TestExpiryUsesInjectedClock(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	s := New(func() time.Time { return now })
	s.Set("session", "open", time.Second)
	if ttl, ok := s.TTL("session"); !ok || ttl != time.Second {
		t.Fatalf("TTL() = %s,%v", ttl, ok)
	}
	now = now.Add(2 * time.Second)
	if _, ok := s.Get("session"); ok {
		t.Fatal("Get(session) ok after expiry")
	}
}

func TestListPushAndRange(t *testing.T) {
	s := New(nil)
	length, err := s.LPush("jobs", "first", "second", "third")
	if err != nil {
		t.Fatalf("LPush() error = %v", err)
	}
	if length != 3 {
		t.Fatalf("LPush() length = %d, want 3", length)
	}
	got, ok := s.LRange("jobs", 0, -1)
	if !ok {
		t.Fatal("LRange() ok = false")
	}
	want := []string{"third", "second", "first"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LRange()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
