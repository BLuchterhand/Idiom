package agent

import "idiom/internal/runtime/agent/runtimecall"

type RuntimeCallTransportMode = runtimecall.TransportMode

const (
	RuntimeCallTransportModeTextual     = runtimecall.TransportModeTextual
	RuntimeCallTransportModeTextualJSON = runtimecall.TransportModeTextualJSON
	RuntimeCallTransportModeNative      = runtimecall.TransportModeNative
)

type RuntimeCallPolicyInput = runtimecall.PolicyInput
type RuntimeCallPolicy = runtimecall.Policy

func DeriveRuntimeCallPolicy(input RuntimeCallPolicyInput) RuntimeCallPolicy {
	return runtimecall.Derive(input)
}

func cloneBoolPtr(value *bool) *bool {
	return runtimecall.CloneBoolPtr(value)
}
