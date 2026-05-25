package server

import (
	"testing"

	"redisclone/internal/resp"
	"redisclone/internal/store"
)

func TestPingAndEcho(t *testing.T) {
	h := New(store.New(nil))
	if got := resp.Encode(h.Execute([]string{"PING"})); got != "+PONG\r\n" {
		t.Fatalf("PING = %q", got)
	}
	if got := resp.Encode(h.Execute([]string{"ECHO", "hello"})); got != "$5\r\nhello\r\n" {
		t.Fatalf("ECHO = %q", got)
	}
}

func TestSetGetIncr(t *testing.T) {
	h := New(store.New(nil))
	if got := resp.Encode(h.Execute([]string{"SET", "visits", "1"})); got != "+OK\r\n" {
		t.Fatalf("SET = %q", got)
	}
	if got := resp.Encode(h.Execute([]string{"GET", "visits"})); got != "$1\r\n1\r\n" {
		t.Fatalf("GET = %q", got)
	}
	if got := resp.Encode(h.Execute([]string{"INCR", "visits"})); got != ":2\r\n" {
		t.Fatalf("INCR = %q", got)
	}
}

func TestListRange(t *testing.T) {
	h := New(store.New(nil))
	h.Execute([]string{"LPUSH", "jobs", "one", "two"})
	if got := resp.Encode(h.Execute([]string{"LRANGE", "jobs", "0", "-1"})); got != "*2\r\n$3\r\ntwo\r\n$3\r\none\r\n" {
		t.Fatalf("LRANGE = %q", got)
	}
}

func TestUnknownCommandReturnsError(t *testing.T) {
	h := New(store.New(nil))
	if got := resp.Encode(h.Execute([]string{"NOPE"})); got != "-ERR unknown command NOPE\r\n" {
		t.Fatalf("unknown command = %q", got)
	}
}