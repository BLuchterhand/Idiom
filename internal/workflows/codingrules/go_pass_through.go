package codingrules

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"

	rulescontract "idiom/internal/contracts/codingrules"
)

const inlineAllowPrefix = "idiom:allow"

func checkGoPassThroughWrappers(ctx context.Context, root string, files []string, rule rulescontract.EffectiveRule, suppressions []rulescontract.Suppression) ([]rulescontract.Finding, int, error) {
	var findings []rulescontract.Finding
	suppressed := 0

	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}

		fileFindings, fileSuppressed, err := checkGoPassThroughFile(root, file, rule, suppressions)
		if err != nil {
			return nil, 0, err
		}

		findings = append(findings, fileFindings...)
		suppressed += fileSuppressed
	}

	return findings, suppressed, nil
}

func checkGoPassThroughFile(root string, absPath string, rule rulescontract.EffectiveRule, suppressions []rulescontract.Suppression) ([]rulescontract.Finding, int, error) {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return nil, 0, err
	}
	rel = filepath.ToSlash(filepath.Clean(rel))

	body, err := os.ReadFile(absPath)
	if err != nil {
		return nil, 0, err
	}

	opts := *rule.Options.PassThroughWrapper
	if opts.IgnoreGenerated && generatedGoFile(body) {
		return nil, 0, nil
	}
	if opts.IgnoreTests && strings.HasSuffix(rel, "_test.go") {
		return nil, 0, nil
	}
	if matchesAnyRulePattern(rule.Exclude, rel) ||
		(len(rule.Include) > 0 && !matchesAnyRulePattern(rule.Include, rel)) {
		return nil, 0, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, body, parser.ParseComments)
	if err != nil {
		return nil, 0, fmt.Errorf("parse Go file %s: %w", rel, err)
	}

	var findings []rulescontract.Finding
	suppressed := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		finding, ok, inlineSuppressed := passThroughWrapperFinding(rel, fset, file, fn, rule)
		if inlineSuppressed {
			suppressed++
			continue
		}
		if !ok {
			continue
		}
		if policySuppressed(finding, rule, suppressions) {
			suppressed++
			continue
		}

		findings = append(findings, finding)
	}

	return findings, suppressed, nil
}

func passThroughWrapperFinding(rel string, fset *token.FileSet, file *ast.File, fn *ast.FuncDecl, rule rulescontract.EffectiveRule) (rulescontract.Finding, bool, bool) {
	opts := *rule.Options.PassThroughWrapper
	if len(fn.Body.List) != opts.MaxBodyStatements || len(fn.Body.List) != 1 {
		return rulescontract.Finding{}, false, false
	}

	paramNames := funcParamNames(fn)
	if len(paramNames) == 0 {
		return rulescontract.Finding{}, false, false
	}

	call, callKind, ok := singleCall(fn.Body.List[0])
	if !ok {
		return rulescontract.Finding{}, false, false
	}

	callee := callName(call.Fun)
	if callee == "" || isBuiltinCall(callee) || callee == fn.Name.Name {
		return rulescontract.Finding{}, false, false
	}

	forwardingDetail, ok := forwardedParamsWithOnlyDefaultArgs(call, paramNames, fn.Name.Name, callee)
	if !ok {
		return rulescontract.Finding{}, false, false
	}

	if inlineAllowed(fset, file, fn, rule) {
		return rulescontract.Finding{}, false, true
	}

	pos := fset.Position(fn.Name.Pos())
	symbol := goSymbolName(fn)

	return rulescontract.Finding{
		RuleID:      rule.ID,
		Language:    rulescontract.LanguageGo,
		Severity:    rule.Severity,
		Enforcement: rule.Enforcement,
		Path:        rel,
		Line:        pos.Line,
		Column:      pos.Column,
		Symbol:      symbol,
		Message:     fmt.Sprintf("%s appears to be a pass-through wrapper around %s", symbol, callee),
		Evidence: []rulescontract.Evidence{
			{Kind: rulescontract.EvidenceSingleStatement, Path: rel, Line: pos.Line},
			{Kind: callKind, Detail: callee, Path: rel, Line: fset.Position(call.Pos()).Line},
			{Kind: rulescontract.EvidenceSameArgumentForwarding, Detail: forwardingDetail, Path: rel, Line: fset.Position(call.Pos()).Line},
			{Kind: rulescontract.EvidenceNoValidation},
			{Kind: rulescontract.EvidenceNoErrorContext},
			{Kind: rulescontract.EvidenceNoTypeTranslation},
			{Kind: rulescontract.EvidenceNoObservability},
			{Kind: rulescontract.EvidenceNoPermissionCheck},
		},
		SuggestedAction: "Inline the call, or add validation, translation, error context, observability, permission checks, or an allowed boundary/interface suppression rationale. Compatibility migration wrappers are not valid suppressions.",
		SuppressionKey:  rulescontract.FindingSuppressionKey(rule.ID, rel, symbol),
	}, true, false
}

func funcParamNames(fn *ast.FuncDecl) []string {
	if fn.Type == nil || fn.Type.Params == nil {
		return nil
	}

	var names []string
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			return nil
		}
		for _, name := range field.Names {
			if name.Name == "_" {
				return nil
			}
			names = append(names, name.Name)
		}
	}

	return names
}

func singleCall(stmt ast.Stmt) (*ast.CallExpr, rulescontract.EvidenceKind, bool) {
	switch typed := stmt.(type) {
	case *ast.ReturnStmt:
		if len(typed.Results) != 1 {
			return nil, "", false
		}
		call, ok := typed.Results[0].(*ast.CallExpr)
		return call, rulescontract.EvidenceSingleReturnCall, ok
	case *ast.ExprStmt:
		call, ok := typed.X.(*ast.CallExpr)
		return call, rulescontract.EvidenceSingleExpressionCall, ok
	default:
		return nil, "", false
	}
}

func forwardedParamsWithOnlyDefaultArgs(call *ast.CallExpr, paramNames []string, wrapperName string, callee string) (string, bool) {
	paramIndex := 0
	projection := make([]string, 0, len(call.Args))
	defaultArgUsed := false

	for _, arg := range call.Args {
		expr := arg
		if ellipsis, ok := arg.(*ast.Ellipsis); ok {
			expr = ellipsis.Elt
		}

		if paramIndex < len(paramNames) {
			if ident, ok := expr.(*ast.Ident); ok && ident.Name == paramNames[paramIndex] {
				projection = append(projection, ident.Name)
				paramIndex++

				continue
			}
		}

		defaultArg, ok := defaultArgumentLiteral(expr)
		if !ok {
			return "", false
		}
		defaultArgUsed = true

		projection = append(projection, defaultArg)
	}

	if paramIndex != len(paramNames) {
		return "", false
	}
	if defaultArgUsed && !defaultArgumentWrapperCandidate(wrapperName, callee) {
		return "", false
	}

	return strings.Join(projection, ", "), true
}

func defaultArgumentLiteral(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.Ident:
		if typed.Name == "nil" {
			return typed.Name, true
		}
	}

	return "", false
}

func defaultArgumentWrapperCandidate(wrapperName string, callee string) bool {
	calleeBase := callee
	if idx := strings.LastIndex(calleeBase, "."); idx >= 0 {
		calleeBase = calleeBase[idx+1:]
	}

	wrapperName = strings.TrimSpace(wrapperName)
	calleeBase = strings.TrimSpace(calleeBase)
	if wrapperName == "" || calleeBase == "" {
		return false
	}

	return strings.HasPrefix(calleeBase, wrapperName)
}

func callName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		prefix := callName(typed.X)
		if prefix == "" {
			return typed.Sel.Name
		}

		return prefix + "." + typed.Sel.Name
	default:
		return ""
	}
}

func isBuiltinCall(name string) bool {
	switch name {
	case "append", "cap", "close", "complex", "copy", "delete", "imag", "len", "make", "new", "panic", "print", "println", "real", "recover":
		return true
	default:
		return false
	}
}

func goSymbolName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}

	return receiverTypeName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	default:
		return "receiver"
	}
}

func inlineAllowed(fset *token.FileSet, file *ast.File, fn *ast.FuncDecl, rule rulescontract.EffectiveRule) bool {
	start := fset.Position(fn.Pos()).Line
	end := fset.Position(fn.End()).Line
	for _, group := range file.Comments {
		groupStart := fset.Position(group.Pos()).Line
		groupEnd := fset.Position(group.End()).Line
		if groupEnd < start-3 || groupStart > end {
			continue
		}

		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), "/*"))
			text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
			if !strings.Contains(text, inlineAllowPrefix) || !strings.Contains(text, string(rule.ID)) {
				continue
			}

			_, rationale, hasRationale := strings.Cut(text, "--")
			return hasRationale && inlineSuppressionAllowed(rationale, rule.AllowWhen)
		}
	}

	return false
}

func inlineSuppressionAllowed(rationale string, allowed []rulescontract.AllowanceReason) bool {
	rationale = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(rationale), "_", " "))
	if rationale == "" {
		return false
	}

	for _, reason := range allowed {
		if inlineRationaleMatchesReason(rationale, reason) {
			return true
		}
	}

	return false
}

func inlineRationaleMatchesReason(rationale string, reason rulescontract.AllowanceReason) bool {
	switch reason {
	case rulescontract.AllowImplementsInterface:
		return strings.Contains(rationale, "implements interface") ||
			strings.Contains(rationale, "implements an interface") ||
			strings.Contains(rationale, "interface implementation") ||
			strings.Contains(rationale, "required by interface")
	case rulescontract.AllowBoundaryAdapter:
		return strings.Contains(rationale, "boundary adapter") ||
			strings.Contains(rationale, "required boundary adapter") ||
			strings.Contains(rationale, "adapter required by")
	case rulescontract.AllowValidation:
		return strings.Contains(rationale, "validation") || strings.Contains(rationale, "validate")
	case rulescontract.AllowErrorContext:
		return strings.Contains(rationale, "error context") || strings.Contains(rationale, "wraps error") || strings.Contains(rationale, "error wrapping")
	case rulescontract.AllowObservability:
		return strings.Contains(rationale, "observability") || strings.Contains(rationale, "telemetry") || strings.Contains(rationale, "logging")
	case rulescontract.AllowPermissionCheck:
		return strings.Contains(rationale, "permission check") || strings.Contains(rationale, "permissions")
	default:
		return false
	}
}

func policySuppressed(finding rulescontract.Finding, rule rulescontract.EffectiveRule, suppressions []rulescontract.Suppression) bool {
	for _, suppression := range suppressions {
		if suppression.RuleID != rule.ID {
			continue
		}
		if filepath.ToSlash(filepath.Clean(suppression.Path)) != finding.Path {
			continue
		}
		if strings.TrimSpace(suppression.Symbol) != "" && suppression.Symbol != finding.Symbol {
			continue
		}
		if !slices.Contains(rule.AllowWhen, suppression.Reason) {
			continue
		}

		return true
	}

	return false
}

func matchesAnyRulePattern(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if matchPathPattern(pattern, rel) {
			return true
		}
	}
	return false
}

func matchPathPattern(pattern string, rel string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	rel = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(rel), "./"))
	if pattern == "" {
		return false
	}
	if pattern == rel {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		if ok, _ := filepath.Match(suffix, rel); ok {
			return true
		}

		return strings.HasSuffix(rel, "/"+suffix) || matchPathPattern(suffix, filepath.Base(rel))
	}

	ok, err := filepath.Match(pattern, rel)
	if err == nil && ok {
		return true
	}
	ok, err = filepath.Match(pattern, filepath.Base(rel))
	return err == nil && ok
}

func generatedGoFile(data []byte) bool {
	head := string(data)
	if len(head) > 4096 {
		head = head[:4096]
	}

	return strings.Contains(head, "Code generated") && strings.Contains(head, "DO NOT EDIT")
}
