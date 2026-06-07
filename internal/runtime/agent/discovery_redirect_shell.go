package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
)

// considerShell handles shell-command redirects. Two shapes are covered:
//
//   - `sed -n 'A,Bp' FILE` -> read_file(path=FILE, start_line=A, end_line=B).
//     Strictly generic: no language-specific replacement is needed.
//   - `wc [-l|-c|-w] FILE` -> file_stats(path=FILE). Strictly generic:
//     the model is asking for bounded counts, not arbitrary shell.
//   - `grep [-n|-rn|-r] "IDENT" [FILE]` with an identifier-shaped pattern
//     -> language symbol tool (references if scoped to a file, workspace
//     symbol otherwise).
//
// Anything else -- free-form regex grep, pipelines, scripts -- falls through
// to normal execution.
func (d *DiscoveryRedirect) considerShell(req workspace.ToolRequest, ws *workspace.Manager) RedirectDecision {
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		return RedirectDecision{}
	}
	if d.firedRedirects[redirectFingerprint(req)] {
		return RedirectDecision{}
	}

	if match := matchShellSedRange(cmd); match != nil {
		msg := fmt.Sprintf(
			"The request reads a file range via shell. Bounded-read capability is available through read_file; it returns structured output and skips the shell permission prompt.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match.path, match.start, match.end)
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|sed_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell range-read blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellHeadTailFile(cmd); match != nil {
		msg := fmt.Sprintf(
			"The request reads a file prefix/suffix via shell. Bounded-read capability is available through read_file; structured output, no permission prompt.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q,\"start_line\":%d,\"end_line\":%d}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match.path, match.start, match.end)
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|headtail_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell prefix/suffix read blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellCatWholeFile(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request dumps an entire file via shell. Bounded-read capability is available through read_file so the result is structured and observable by the harness.\n"+
				"candidate_request: {\"tool\":\"read_file\",\"path\":%q}\n"+
				"direct_tool_fallback: if the bounded read returns nothing, the original command remains available.",
			match)
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|cat_read_file",
			capability:        workspace.Capability("read_file"),
			summary:           "Redirected to read_file",
			hardRejectSummary: "Shell cat blocked by bounded-read capability",
			message:           msg,
		})
	}

	if match := matchShellFindName(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request searches for files by name via shell. File-name search capability is available through search_files; it returns stable handles.\n"+
				"candidate_request: {\"tool\":\"search_files\",\"pattern\":%q}\n"+
				"direct_tool_fallback: if file-name search returns nothing, the original command remains available.",
			match)
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|find_search_files",
			capability:        workspace.Capability("search_files"),
			summary:           "Redirected to search_files",
			hardRejectSummary: "Shell find blocked by file-name search capability",
			message:           msg,
		})
	}

	if match := matchShellWCStats(cmd); match != "" {
		msg := fmt.Sprintf(
			"The request counts file size/lines via shell. File-stat capability is available through file_stats; it returns structured file, byte, and line counts without a shell prompt.\n"+
				"candidate_request: {\"tool\":\"file_stats\",\"path\":%q}\n"+
				"direct_tool_fallback: if file stats do not answer the question, the original command remains available.",
			match)
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              "shell|file_stats",
			capability:        workspace.Capability("file_stats"),
			summary:           "Redirected to file_stats",
			hardRejectSummary: "Shell wc blocked by file-stat capability",
			message:           msg,
		})
	}

	if match := matchShellGrepIdentifier(cmd); match != nil {
		lang, ok := languageForShellGrep(match, ws)
		if !ok {
			return RedirectDecision{}
		}
		if !languageActive(ws, lang) {
			return RedirectDecision{}
		}
		// Prefer references when the model already named a file (that's
		// the "where is X used in this file" question); workspace_symbol
		// otherwise.
		primary := workspace.CapWorkspaceSymbol
		if match.path != "" {
			primary = workspace.CapReferences
		}
		replacement := lang.Tool(primary)
		if replacement == "" {
			// Fall back to the other side of the symbol pair.
			alt := workspace.CapFindSymbol
			if primary == workspace.CapReferences {
				alt = workspace.CapFindSymbol
			}
			replacement = lang.Tool(alt)
			if replacement == "" {
				return RedirectDecision{}
			}
			primary = alt
		}
		kind := "shell|" + lang.Name + "|" + string(primary)

		guide := ""
		if !d.decisionTablesShown[lang.Name] {
			d.decisionTablesShown[lang.Name] = true
			guide = lang.DecisionGuide
		}

		var b strings.Builder
		fmt.Fprintf(&b, "The request greps for an identifier via shell on %s code. Capability route: %s via adapter %s; it returns file, line, and qualified-name for each hit.\n",
			lang.Name, primary, replacement)
		if match.path != "" {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q,\"path\":%q}\n", replacement, match.pattern, match.path)
		} else {
			fmt.Fprintf(&b, "candidate_request: {\"tool\":%q,\"pattern\":%q}\n", replacement, match.pattern)
		}
		b.WriteString("direct_tool_fallback: if the capability route returns nothing, the original command remains available.")
		return d.shellRedirectDecision(req, shellRedirect{
			kind:              kind,
			capability:        primary,
			summary:           "Redirected to " + replacement,
			hardRejectSummary: fmt.Sprintf("Shell grep blocked by %s capability", primary),
			message:           b.String(),
			language:          lang.Name,
			decisionGuide:     guide,
		})
	}

	return RedirectDecision{}
}

type shellRedirect struct {
	kind              string
	capability        workspace.Capability
	summary           string
	hardRejectSummary string
	message           string
	language          string
	decisionGuide     string
}

// Shell redirects protect a permission boundary: shell grep/find/cat can
// bypass the structured discovery contract. Unlike generic code-read
// redirects, repeated shell redirects may become hard rejections.
const turnWideShellRedirectHardBlockAt = 3

func (d *DiscoveryRedirect) shellRedirectDecision(req workspace.ToolRequest, redirect shellRedirect) RedirectDecision {
	d.firedRedirects[redirectFingerprint(req)] = true
	d.kindFireCount[redirect.kind]++
	hardReject := d.kindFireCount[redirect.kind] >= turnWideShellRedirectHardBlockAt

	result := workspace.ToolResult{Tool: req.Tool, Summary: redirect.summary, Output: redirect.message}
	if hardReject {
		result.Summary = redirect.hardRejectSummary
		result.Error = redirect.message
		result.Output = ""
	}
	return RedirectDecision{
		ShouldRedirect: true,
		HardReject:     hardReject,
		Result:         result,
		DecisionGuide:  redirect.decisionGuide,
		Language:       redirect.language,
		Capability:     redirect.capability,
	}
}
