package localops

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestBuildBenchmarkReportAssemblesMetadataOnlyEvidence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	report, err := BuildBenchmarkReport(BenchmarkOptions{
		ConfigPath:    filepath.Join(root, ".idiom", "config.json"),
		Profile:       "eval",
		OutputPath:    filepath.Join(root, "reports", "local-ops.json"),
		TelemetryPath: filepath.Join(root, "logs", "telemetry.jsonl"),
	}, QuickstartDeps{})
	if err != nil {
		t.Fatalf("build benchmark report: %v", err)
	}
	if report.SchemaVersion != BenchmarkSchemaVersion {
		t.Fatalf("unexpected schema version: %#v", report)
	}
	if len(report.Profiles) != 1 || report.Profiles[0].Name != "eval" {
		t.Fatalf("expected eval profile, got %#v", report.Profiles)
	}
	if report.Replay.Status != "unsupported" || len(report.Replay.Blockers) == 0 {
		t.Fatalf("expected explicit unsupported replay evidence, got %#v", report.Replay)
	}
	if report.Telemetry == nil || report.Telemetry.Path == "" || report.Telemetry.ID == "" {
		t.Fatalf("expected telemetry path evidence, got %#v", report.Telemetry)
	}
	if !report.Privacy.MetadataOnly || report.Privacy.RawPromptCaptured || report.Privacy.RawFileBodyCaptured || report.Privacy.SecretCaptureAllowed {
		t.Fatalf("benchmark report must stay metadata-only, got %#v", report.Privacy)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if finding := ForbiddenEvidenceLeak(string(body)); finding != "" {
		t.Fatalf("benchmark report leaked forbidden evidence %q in %s", finding, string(body))
	}
}

func TestBenchmarkReportJSONContractKeys(t *testing.T) {
	report := BenchmarkReport{
		SchemaVersion: BenchmarkSchemaVersion,
		Quickstart: &QuickstartSmoke{
			ProductEvidenceSafeOutput: ActivationProductEvidence{},
			Cleanup: ActivationCleanupEvidence{
				RemovedArtifactCount: 2,
			},
		},
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	rendered := string(body)
	for _, key := range []string{
		`"schema_version"`,
		`"quickstart_smoke"`,
		`"product_evidence_safe_output"`,
		`"removed_artifact_count"`,
	} {
		if !strings.Contains(rendered, key) {
			t.Fatalf("missing JSON contract key %s in %s", key, rendered)
		}
	}
}

func TestBuildBenchmarkReportRecordsQuickstartSetupFailure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	report, err := BuildBenchmarkReport(BenchmarkOptions{
		ConfigPath:        filepath.Join(root, ".idiom", "config.json"),
		Profile:           "sidecar",
		QuickstartSmoke:   true,
		QuickstartTask:    "localize quickstart smoke",
		QuickstartCleanup: "report-only",
	}, QuickstartDeps{
		LoadConfig: func(string) (config.Config, error) {
			return config.Config{}, errors.New("load /Users/ben/CustomerX-private-repo failed")
		},
	})
	if err != nil {
		t.Fatalf("build benchmark report: %v", err)
	}
	if report.Quickstart == nil || report.Quickstart.Status != "blocked" {
		t.Fatalf("expected blocked quickstart evidence, got %#v", report.Quickstart)
	}
	if len(report.Blockers) == 0 {
		t.Fatalf("expected quickstart blocker in report: %#v", report)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if finding := ForbiddenEvidenceLeak(string(body)); finding != "" {
		t.Fatalf("blocked quickstart evidence leaked forbidden data %q in %s", finding, string(body))
	}
}
