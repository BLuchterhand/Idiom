package workspace

import (
	"path/filepath"
	"strings"

	semanticlanguage "idiom/internal/repository/semantic/language"
)

// VerificationObservation is the workspace-owned classification of whether a
// tool request/result represents verification evidence. Runtime layers consume
// these facts; lane policy still decides when verification is required.
type VerificationObservation struct {
	Attempted                     bool
	Successful                    bool
	Command                       string
	Verification                  ShellVerificationFamily
	FullSuite                     bool
	BuildOrTest                   bool
	PatchHygieneOnly              bool
	HTMLSmokeForPendingHTMLWrites bool
}

func AnalyzeVerificationRequest(req ToolRequest) VerificationObservation {
	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameSourceSanity || semanticlanguage.IsDiagnosticToolName(tool) {
		return VerificationObservation{Attempted: true}
	}
	if tool != ToolNameShell {
		return VerificationObservation{}
	}
	return analyzeShellVerificationCommand(strings.TrimSpace(req.Command))
}

func AnalyzeVerificationResult(result ToolResult) VerificationObservation {
	tool := strings.TrimSpace(result.Tool)
	if tool == ToolNameSourceSanity {
		return VerificationObservation{
			Attempted:  true,
			Successful: strings.TrimSpace(result.Error) == "" && sourceSanityResultUsesLanguageDiagnostics(result),
		}
	}
	if semanticlanguage.IsDiagnosticToolName(tool) {
		return VerificationObservation{
			Attempted:  true,
			Successful: strings.TrimSpace(result.Error) == "",
		}
	}
	if tool != ToolNameShell {
		return VerificationObservation{}
	}

	observed := analyzeShellVerificationFacts(result)
	if observed.Attempted {
		observed.Successful = strings.TrimSpace(result.Error) == ""
	}
	return observed
}

func AnalyzePostWriteVerificationResult(result ToolResult, pendingPaths []string) VerificationObservation {
	observed := AnalyzeVerificationResult(result)
	if observed.Successful {
		return observed
	}

	if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Tool) != ToolNameShell {
		return observed
	}

	if result.Facts == nil || strings.TrimSpace(result.Facts.ShellCommand) == "" {
		return observed
	}

	if !shellOutputVerifiesPendingHTMLArtifacts(result.Output, pendingPaths) {
		return observed
	}

	observed.Attempted = true
	observed.Successful = true
	observed.Command = strings.TrimSpace(result.Facts.ShellCommand)
	observed.HTMLSmokeForPendingHTMLWrites = true
	return observed
}

func analyzeShellVerificationCommand(command string) VerificationObservation {
	analysis := AnalyzeShellCommand(command)
	if analysis.Verification == ShellVerificationNone {
		return VerificationObservation{}
	}
	return VerificationObservation{
		Attempted:        true,
		Command:          command,
		Verification:     analysis.Verification,
		FullSuite:        analysis.FullSuite,
		BuildOrTest:      analysis.BuildOrTest,
		PatchHygieneOnly: shellVerificationCommandIsPatchHygieneOnly(command, analysis),
	}
}

func analyzeShellVerificationFacts(result ToolResult) VerificationObservation {
	if result.Facts == nil {
		return VerificationObservation{}
	}

	command := strings.TrimSpace(result.Facts.ShellCommand)
	if command == "" {
		return VerificationObservation{}
	}

	return analyzeShellVerificationCommand(command)
}

func shellVerificationCommandIsPatchHygieneOnly(command string, analysis ShellCommandAnalysis) bool {
	return strings.Contains(strings.ToLower(command), "git diff --check") && !analysis.BuildOrTest
}

func sourceSanityResultUsesLanguageDiagnostics(result ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false
	}

	if strings.Contains(text, "generic fallback") ||
		strings.Contains(text, "generic_source_sanity") ||
		strings.Contains(text, "generic delimiter") ||
		strings.Contains(text, "generic source sanity") {

		return false
	}

	for _, tool := range semanticlanguage.DiagnosticToolNames() {
		if strings.Contains(text, strings.ToLower(strings.TrimSpace(tool))) {
			return true
		}
	}

	return false
}

func shellOutputVerifiesPendingHTMLArtifacts(output string, pendingPaths []string) bool {
	normalized := make([]string, 0, len(pendingPaths))
	seen := make(map[string]struct{}, len(pendingPaths))
	for _, path := range pendingPaths {
		path = normalizeVerificationPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	if len(normalized) == 0 {
		return false
	}

	for _, path := range normalized {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".html" && ext != ".htm" {
			return false
		}
	}

	lower := strings.ToLower(output)
	if !strings.Contains(lower, "python") ||
		(!strings.Contains(lower, "html.parser") && !strings.Contains(lower, "htmlparser")) {

		return false
	}

	for _, path := range normalized {
		if !shellOutputMentionsVerificationPath(lower, path) {
			return false
		}
	}

	return true
}

func shellOutputMentionsVerificationPath(lowerOutput string, path string) bool {
	normalized := strings.ToLower(normalizeVerificationPath(path))
	if normalized == "" {
		return false
	}
	if strings.Contains(lowerOutput, normalized) {
		return true
	}
	base := strings.ToLower(filepath.Base(normalized))
	return base != "." && base != "" && strings.Contains(lowerOutput, base)
}

func normalizeVerificationPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return filepath.ToSlash(path)
}
