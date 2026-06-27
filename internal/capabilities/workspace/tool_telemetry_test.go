package workspace

import "testing"

func TestAddToolTelemetryAttributionUsesToolSidecarContractID(t *testing.T) {
	fields := map[string]any{}
	AddToolTelemetryAttribution(fields, ToolSidecarEnvelopeVersion, "mcp_stdio", ToolProfileDiscovery, ToolSidecarCaller{}, " context_status ")

	want := BuildToolSidecarContract("context_status", ToolProfileDiscovery, "mcp_stdio").ID
	if fields["contract_id"] != want {
		t.Fatalf("contract_id = %#v, want %q", fields["contract_id"], want)
	}
}
