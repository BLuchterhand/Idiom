package queueadmission

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	queuecontract "idiom/internal/contracts/queue"
	"idiom/internal/platform/config"
)

func TestAcquireReportsDisabledWhenNoPoolResolves(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "")
	t.Setenv("IDIOM_QUEUE_SOCKET", "")

	var stdout, stderr bytes.Buffer

	release := Acquire(config.Config{}, Options{
		Command:        "plan_execute",
		EventPrefix:    queuecontract.CommandEventPrefixPlanExecute,
		TelemetryEvent: "plan_execute_queue",
		Label:          "plan-execute:session",
		JSONEvents:     true,
	}, nil, &stdout, &stderr)
	release()

	out := stdout.String()
	if !strings.Contains(out, `"type":"`+queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueDisabled)+`"`) {
		t.Fatalf("disabled queue event missing, got %q", out)
	}

	if !strings.Contains(out, `"reason_class":"pool_disabled"`) {
		t.Fatalf("disabled queue event should classify reason, got %q", out)
	}

	if stderr.Len() != 0 {
		t.Fatalf("disabled queue should not emit stderr, got %q", stderr.String())
	}
}

func TestAcquireWithResultRequiredModeBlocksUnavailableQueue(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

	var stdout, stderr bytes.Buffer
	result := AcquireWithResult(config.Config{}, Options{
		Command:     "plan_execute",
		EventPrefix: queuecontract.CommandEventPrefixPlanExecute,
		JSONEvents:  true,
		Mode:        queuecontract.AdmissionModeRequired,
	}, nil, &stdout, &stderr)
	result.Release()

	if result.Allowed {
		t.Fatalf("required queue admission should block unavailable queue: %#v", result)
	}
	if result.State != queuecontract.AdmissionStateUnavailable {
		t.Fatalf("state = %q, want unavailable", result.State)
	}
	if !strings.Contains(stdout.String(), `"type":"`+queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueUnavailable)+`"`) {
		t.Fatalf("missing unavailable JSON event: %q", stdout.String())
	}
}

func TestAcquireWithResultRequiredModeBlocksNestedCrossPool(t *testing.T) {
	t.Setenv("IDIOM_QUEUE_POOL", "qwen-coder")
	t.Setenv("IDIOM_QUEUE_HELD_POOLS", "qwen-35b")
	t.Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))

	var stdout, stderr bytes.Buffer
	result := AcquireWithResult(config.Config{}, Options{
		Command:     "plan_execute",
		EventPrefix: queuecontract.CommandEventPrefixPlanExecute,
		JSONEvents:  true,
		Mode:        queuecontract.AdmissionModeRequired,
	}, nil, &stdout, &stderr)
	result.Release()

	if result.Allowed {
		t.Fatalf("required queue admission should block nested cross-pool bypass: %#v", result)
	}
	if result.State != queuecontract.AdmissionStateNestedCrossPool {
		t.Fatalf("state = %q, want %q", result.State, queuecontract.AdmissionStateNestedCrossPool)
	}
	out := stdout.String()
	if strings.Contains(out, `"type":"`+queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueAcquired)+`"`) ||
		!strings.Contains(out, `"type":"`+queuecontract.CommandAdmissionEventName(queuecontract.CommandEventPrefixPlanExecute, queuecontract.AdmissionEventQueueDegraded)+`"`) {
		t.Fatalf("nested cross-pool admission must emit degraded, not acquired: %q", out)
	}
	if !strings.Contains(out, `"degraded":true`) {
		t.Fatalf("nested cross-pool admission should mark degraded: %q", out)
	}
}

func TestReasonClassNormalizesSharedQueueReasons(t *testing.T) {
	cases := map[string]queuecontract.ReasonClass{
		"nested_same_pool":              queuecontract.ReasonClassNestedSamePool,
		"nested_cross_pool":             queuecontract.ReasonClassNestedCrossPool,
		"unknown pool qwen-35b":         queuecontract.ReasonClassUnknownPool,
		"daemon_unreachable":            queuecontract.ReasonClassDaemonUnreachable,
		"connection refused":            queuecontract.ReasonClassDaemonUnreachable,
		"context deadline exceeded":     queuecontract.ReasonClassTimeoutOrCanceled,
		"unexpected queue daemon error": queuecontract.ReasonClassQueueError,
	}
	for reason, want := range cases {
		if got := queuecontract.ClassifyReason(reason); got != want {
			t.Fatalf("ClassifyReason(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestQueueAdmissionWaitTimeoutDefaultsAndHonorsOverride(t *testing.T) {
	if got := queueAdmissionWaitTimeout(Options{}); got != defaultQueueAdmissionWaitTimeout {
		t.Fatalf("queueAdmissionWaitTimeout default = %s, want %s", got, defaultQueueAdmissionWaitTimeout)
	}

	custom := 2 * time.Second
	if got := queueAdmissionWaitTimeout(Options{WaitTimeout: custom}); got != custom {
		t.Fatalf("queueAdmissionWaitTimeout override = %s, want %s", got, custom)
	}

	if got := queueAdmissionWaitTimeout(Options{WaitTimeout: -time.Second}); got != defaultQueueAdmissionWaitTimeout {
		t.Fatalf("queueAdmissionWaitTimeout negative override = %s, want default %s", got, defaultQueueAdmissionWaitTimeout)
	}
}
