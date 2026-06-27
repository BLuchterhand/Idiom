package queue

import "testing"

func TestAdmissionEventNameMapsStates(t *testing.T) {
	tests := []struct {
		state AdmissionState
		want  AdmissionEventType
	}{
		{AdmissionStateAcquired, AdmissionEventQueueAcquired},
		{AdmissionStateGranted, AdmissionEventQueueAcquired},
		{AdmissionStateNestedSamePool, AdmissionEventQueueAcquired},
		{AdmissionStateNestedCrossPool, AdmissionEventQueueDegraded},
		{AdmissionStateUnavailable, AdmissionEventQueueUnavailable},
		{AdmissionStateDisabled, AdmissionEventQueueDisabled},
		{AdmissionStateDegraded, AdmissionEventQueueDegraded},
		{AdmissionStateError, AdmissionEventQueueError},
		{AdmissionState("unknown"), AdmissionEventQueueError},
	}

	for _, test := range tests {
		if got := AdmissionEventName(test.state); got != test.want {
			t.Fatalf("AdmissionEventName(%q) = %q, want %q", test.state, got, test.want)
		}
	}
}

func TestAdmissionStateAllowsOnlyAcquiredOrSamePoolNestedDispatch(t *testing.T) {
	if !AdmissionStateAllowsDispatch(AdmissionStateNestedSamePool) {
		t.Fatalf("%s should remain dispatchable as an explicit same-pool nested no-op", AdmissionStateNestedSamePool)
	}

	for _, state := range []AdmissionState{AdmissionStateNestedCrossPool, AdmissionStateDegraded} {
		if AdmissionStateAllowsDispatch(state) {
			t.Fatalf("%s must not dispatch under required mode without the requested pool lease", state)
		}
	}
}

func TestClassifyReason(t *testing.T) {
	tests := []struct {
		reason string
		want   ReasonClass
	}{
		{"", ""},
		{"queue_unavailable: no daemon", ReasonClassQueueUnavailable},
		{"lease already held by parent", ReasonClassNestedLease},
		{"pool_disabled by config", ReasonClassPoolDisabled},
		{"nested_same_pool request", ReasonClassNestedSamePool},
		{"nested_cross_pool request", ReasonClassNestedCrossPool},
		{"unknown pool qwen", ReasonClassUnknownPool},
		{"daemon_unreachable: connection refused", ReasonClassDaemonUnreachable},
		{"context canceled", ReasonClassTimeoutOrCanceled},
		{"unexpected queue failure", ReasonClassQueueError},
	}

	for _, test := range tests {
		if got := ClassifyReason(test.reason); got != test.want {
			t.Fatalf("ClassifyReason(%q) = %q, want %q", test.reason, got, test.want)
		}
	}
}

func TestClassifyFailureCode(t *testing.T) {
	tests := []struct {
		code string
		want ReasonClass
	}{
		{"unavailable", ReasonClassQueueUnavailable},
		{"deadline_exceeded", ReasonClassTimeoutOrCanceled},
		{"canceled", ReasonClassTimeoutOrCanceled},
		{"invalid_argument", ReasonClassUnknownPool},
		{"failed_precondition", ReasonClassQueueError},
		{"unknown", ""},
		{"", ""},
	}

	for _, test := range tests {
		if got := ClassifyFailureCode(test.code); got != test.want {
			t.Fatalf("ClassifyFailureCode(%q) = %q, want %q", test.code, got, test.want)
		}
	}
}
