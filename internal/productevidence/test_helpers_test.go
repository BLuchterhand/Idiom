package productevidence

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	restore := setDefaultDependenciesForTest(Dependencies{
		RunMainNoTelemetry:  runProductEvidenceTestMain,
		DemoCommandRunnerID: "productevidence-test-runner",
		Version:             "dev",
	})
	code := m.Run()
	restore()
	os.Exit(code)
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func runProductEvidenceTestMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing command")
		return 2
	}
	switch args[0] {
	case "tool":
		return runProductEvidenceTestTool(args[1:], stdout, stderr)
	case "local-ops":
		if len(args) > 1 && args[1] == "benchmark" {
			return writeProductEvidenceTestJSON(stdout, productEvidenceTestLocalOpsPayload(args))
		}
	}
	fmt.Fprintf(stderr, "unsupported test command %q\n", args[0])
	return 2
}

func runProductEvidenceTestTool(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "missing tool")
		return 2
	}
	tool := args[0]
	switch tool {
	case "schemas":
		return writeProductEvidenceTestJSON(stdout, map[string]any{
			"schema_version": "idiom.tool_schema_export.v1",
			"metadata": map[string]any{
				"client_classifications": []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"},
				"product_signals":        []string{"activation", "integration_proof", "security_privacy"},
			},
			"tools": []map[string]any{{"name": "source_sanity"}, {"name": "context_pack"}},
		})
	case "source_sanity":
		return writeProductEvidenceTestJSON(stdout, productEvidenceTestToolPayload(tool, "default", false))
	case "write_file", "context_pack", "shell":
		return writeProductEvidenceTestJSONWithCode(stdout, productEvidenceTestToolPayload(tool, "discovery", true), 1)
	default:
		return writeProductEvidenceTestJSON(stdout, productEvidenceTestToolPayload(tool, "default", false))
	}
}

func productEvidenceTestToolPayload(tool string, profile string, denied bool) map[string]any {
	payload := map[string]any{
		"Tool":    tool,
		"Summary": "metadata-only test evidence",
		"sidecar": map[string]any{
			"schema_version": "idiom.tool.v1",
			"tool":           tool,
			"profile":        profile,
			"caller": map[string]any{
				"trace_id": "trace:p710-saas",
			},
			"contract": map[string]any{
				"id":             "idiom.tool.v1:" + tool,
				"schema_version": "idiom.tool_contract.v1",
				"transport":      "cli",
				"metadata": map[string]any{
					"client_classifications": []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"},
					"product_signals":        []string{"activation", "integration_proof", "security_privacy"},
				},
			},
			"structured_output": map[string]any{"format": "text"},
			"audit_event_ids":   []string{"audit:test"},
			"error":             nil,
			"freshness":         nil,
		},
	}
	if denied {
		payload["Error"] = "profile_denied"
		sidecar := payload["sidecar"].(map[string]any)
		sidecar["error"] = map[string]any{
			"class":   "profile_denied",
			"code":    "profile_denied",
			"message": "blocked by discovery profile",
		}
		delete(payload, "Summary")
	}
	return payload
}

func productEvidenceTestLocalOpsPayload(args []string) map[string]any {
	payload := map[string]any{
		"schema_version": "idiom.local_ops_benchmark.v1",
		"profiles":       []string{"implement"},
		"summary":        map[string]any{"status": "pass"},
		"privacy": map[string]any{
			"metadata_only":          true,
			"raw_prompt_captured":    false,
			"raw_file_body_captured": false,
			"secret_capture_allowed": false,
		},
	}
	if productEvidenceTestArgsContain(args, "sidecar") {
		payload["profiles"] = []string{"sidecar"}
		payload["quickstart_smoke"] = map[string]any{
			"status":                "pass",
			"client_classification": "open-source",
			"first_useful_packet": map[string]any{
				"tool":                   "context_pack",
				"packet_id":              "packet:test",
				"index_id":               "index:test",
				"freshness_state":        "fresh",
				"candidate_file_count":   1,
				"candidate_symbol_count": 1,
				"candidate_test_count":   1,
				"task":                   map[string]any{"raw_task_captured": false},
			},
			"freshness_check": map[string]any{"state": "fresh"},
			"suggested_verification": map[string]any{
				"suggested_commands": []string{"go test ./..."},
			},
			"product_evidence_safe_output": map[string]any{
				"forbidden_field_check": "pass",
				"artifact_hash":         "hash:test",
			},
			"privacy_redaction_status": map[string]any{
				"private_path_captured": false,
			},
		}
	}
	return payload
}

func productEvidenceTestArgsContain(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func writeProductEvidenceTestJSON(stdout io.Writer, payload map[string]any) int {
	return writeProductEvidenceTestJSONWithCode(stdout, payload, 0)
}

func writeProductEvidenceTestJSONWithCode(stdout io.Writer, payload map[string]any, code int) int {
	body, err := json.Marshal(payload)
	if err != nil {
		return 2
	}
	fmt.Fprintln(stdout, string(body))
	return code
}
