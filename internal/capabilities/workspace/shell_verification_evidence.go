package workspace

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/textutil"
)

func (m *Manager) successfulShellVerificationEvidence(ctx context.Context, command string, executionWorkdir string, workdir string) []toolfacts.VerificationEvidence {
	evidence := successfulCanonicalHTMLVerificationEvidence(command, workdir)

	if pathspecs, ok := workingTreeGitDiffCheckPathspecs(command); ok {
		if paths := m.gitDiffCheckEvidencePaths(ctx, executionWorkdir, pathspecs); len(paths) > 0 {
			evidence = append(evidence, toolfacts.VerificationEvidence{
				Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
				Paths: paths,
			})
		}
	}

	return evidence
}

func successfulCanonicalHTMLVerificationEvidence(command string, workdir string) []toolfacts.VerificationEvidence {
	path, ok := canonicalHTMLParserPath(command)
	if ok {
		resolved := verificationScopesInWorkdir([]string{path}, workdir)
		if len(resolved) == 1 {
			return []toolfacts.VerificationEvidence{{
				Kind:  toolfacts.VerificationEvidenceHTMLParse,
				Paths: resolved,
			}}
		}
	}
	return nil
}

func (m *Manager) gitDiffCheckEvidencePaths(ctx context.Context, workdir string, pathspecs []string) []string {
	rootResult := m.runCommandResult(ctx, commandexec.Spec{
		Mode: commandexec.ModeArgv,
		Dir:  workdir,
		Name: "git",
		Args: []string{"rev-parse", "--show-toplevel"},
	})
	if rootResult.FailureError("") != nil || rootResult.StdoutTruncated {
		return nil
	}
	root := strings.TrimSpace(rootResult.Stdout)
	if root == "" || strings.ContainsAny(root, "\r\n\x00") {
		return nil
	}

	args := []string{"diff", "--name-only", "-z"}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	diffResult := m.runCommandResult(ctx, commandexec.Spec{
		Mode: commandexec.ModeArgv,
		Dir:  workdir,
		Name: "git",
		Args: args,
	})
	if diffResult.FailureError("") != nil || diffResult.StdoutTruncated {
		return nil
	}

	var paths []string
	for _, raw := range strings.Split(diffResult.Stdout, "\x00") {
		// Verification paths pass through the harness's normalized path model.
		// Reject Git names that normalization would rewrite into a different
		// artifact instead of accidentally granting coverage to that artifact.
		if raw == "" || raw != strings.TrimSpace(raw) ||
			strings.Trim(raw, "`'\"") != raw || strings.ContainsAny(raw, "\\\r\n") {
			continue
		}
		display := normalizeVerificationScope(m.pathAccess.DisplayPath(filepath.Join(root, filepath.FromSlash(raw))))
		if display != "" {
			paths = textutil.AppendUnique(paths, display)
		}
	}
	return paths
}

func workingTreeGitDiffCheckPathspecs(command string) ([]string, bool) {
	if !standaloneGitDiffCheckCommand(command) {
		return nil, false
	}
	fields := shellCommandFields(command)

	seenCheck := false
	for i, arg := range fields[2:] {
		switch strings.TrimSpace(arg) {
		case "--check":
			if seenCheck {
				return nil, false
			}
			seenCheck = true
		case "--":
			if !seenCheck {
				return nil, false
			}
			paths := append([]string(nil), fields[i+3:]...)
			for _, path := range paths {
				if normalizeVerificationScope(path) == "" || strings.ContainsAny(path, "*?[]") {
					return nil, false
				}
			}
			return paths, true
		default:
			return nil, false
		}
	}

	if !seenCheck {
		return nil, false
	}
	return nil, true
}

func standaloneGitDiffCheckCommand(command string) bool {
	if AnalyzeShellCommand(command).HasControlSyntax {
		return false
	}
	fields := shellCommandFields(command)
	if len(fields) < 3 || normalizeShellHead(fields[0]) != "git" || strings.TrimSpace(fields[1]) != "diff" {
		return false
	}
	for _, arg := range fields[2:] {
		arg = strings.TrimSpace(arg)
		if arg == "--" {
			return false
		}
		if arg == "--check" {
			return true
		}
	}
	return false
}

func canonicalHTMLParserPath(command string) (string, bool) {
	if AnalyzeShellCommand(command).HasControlSyntax {
		return "", false
	}

	fields := shellCommandFields(command)
	if len(fields) != 3 ||
		normalizeShellHead(fields[0]) != "python3" ||
		strings.TrimSpace(fields[1]) != "-c" {

		return "", false
	}

	program := fields[2]
	lines := strings.Split(strings.TrimSuffix(program, "\n"), "\n")
	if len(lines) != 5 ||
		lines[0] != "from html.parser import HTMLParser" ||
		!strings.HasPrefix(lines[1], "path = ") {

		return "", false
	}

	path, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(lines[1], "path = ")))
	if err != nil {
		return "", false
	}
	path = normalizeVerificationScope(path)
	if path == "" {
		return "", false
	}
	if command != HTMLParserVerificationCommand(path) {
		return "", false
	}
	return path, true
}

// HTMLParserVerificationCommand returns the exact shell command recognized as
// producer-owned HTML parser verification, or an empty string for an invalid
// or non-HTML workspace path. Keeping generation and recognition together
// prevents argv-equivalent strings with different /bin/sh semantics from being
// admitted as verification requests.
func HTMLParserVerificationCommand(path string) string {
	path = normalizeVerificationScope(path)
	if path == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".html" && ext != ".htm" {
		return ""
	}

	return "python3 -c " + singleQuoteShellWord(canonicalHTMLParserProgram(path))
}

func singleQuoteShellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func canonicalHTMLParserProgram(path string) string {
	return "from html.parser import HTMLParser\n" +
		"path = " + strconv.Quote(path) + "\n" +
		"with open(path, encoding='utf-8') as f:\n" +
		"    HTMLParser().feed(f.read())\n" +
		"print('HTML parsed OK: ' + path)\n"
}
