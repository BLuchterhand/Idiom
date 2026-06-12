package queueadmission

import (
	"bytes"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestAcquireReportsDisabledWhenNoPoolResolves(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "")
	t.Setenv("IDIOM_QUEUE_SOCKET", "")

	var stdout, stderr bytes.Buffer

	release := Acquire(config.Config{}, Options{
		Command:        "plan_execute",
		EventPrefix:    "idiom.plan_execute",
		TelemetryEvent: "plan_execute_queue",
		Label:          "plan-execute:session",
		JSONEvents:     true,
	}, nil, &stdout, &stderr)
	release()

	out := stdout.String()
	if !strings.Contains(out, `"type":"idiom.plan_execute.queue_disabled"`) {
		t.Fatalf("disabled queue event missing, got %q", out)
	}

	if !strings.Contains(out, `"reason_class":"pool_disabled"`) {
		t.Fatalf("disabled queue event should classify reason, got %q", out)
	}

	if stderr.Len() != 0 {
		t.Fatalf("disabled queue should not emit stderr, got %q", stderr.String())
	}
}

func TestReasonClassNormalizesSharedQueueReasons(t *testing.T) {
	cases := map[string]string{
		"nested_same_pool":              "nested_same_pool",
		"nested_cross_pool":             "nested_cross_pool",
		"unknown pool qwen-35b":         "unknown_pool",
		"daemon_unreachable":            "daemon_unreachable",
		"connection refused":            "daemon_unreachable",
		"context deadline exceeded":     "timeout_or_canceled",
		"unexpected queue daemon error": "queue_error",
	}
	for reason, want := range cases {
		if got := ReasonClass(reason); got != want {
			t.Fatalf("ReasonClass(%q) = %q, want %q", reason, got, want)
		}
	}
}
