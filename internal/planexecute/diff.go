package planexecute

import (
	"fmt"
	"strings"
)

func unifiedDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	bLines, bHadNL := splitLines(before)
	aLines, aHadNL := splitLines(after)
	ops := diffScript(bLines, aLines)
	if len(ops) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n", path)
	fmt.Fprintf(&out, "+++ b/%s\n", path)
	for _, h := range buildHunks(ops, 3) {
		renderHunk(&out, ops, bLines, aLines, h, bHadNL, aHadNL)
	}
	return out.String()
}

func UnifiedDiff(path, before, after string) string {
	return unifiedDiff(path, before, after)
}

func splitLines(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	hadNL := strings.HasSuffix(s, "\n")
	if hadNL {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n"), hadNL
}

type diffOp struct {
	kind byte
	bIdx int
	aIdx int
}

func diffScript(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, diffOp{'-', i, -1})
			i++
		default:
			ops = append(ops, diffOp{'+', -1, j})
			j++
		}
	}
	for i < n {
		ops = append(ops, diffOp{'-', i, -1})
		i++
	}
	for j < m {
		ops = append(ops, diffOp{'+', -1, j})
		j++
	}
	return ops
}

type hunk struct {
	startOp, endOp int
}

func buildHunks(ops []diffOp, ctx int) []hunk {
	var hunks []hunk
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i - ctx
		if start < 0 {
			start = 0
		}
		j := i
		for j < len(ops) {
			if ops[j].kind != ' ' {
				j++
				continue
			}
			k := j
			for k < len(ops) && ops[k].kind == ' ' && k-j < 2*ctx {
				k++
			}
			if k < len(ops) && ops[k].kind != ' ' {
				j = k
				continue
			}
			break
		}
		end := j
		trailing := 0
		for end < len(ops) && ops[end].kind == ' ' && trailing < ctx {
			end++
			trailing++
		}
		hunks = append(hunks, hunk{startOp: start, endOp: end})
		i = end
	}
	return hunks
}

func renderHunk(out *strings.Builder, ops []diffOp, before, after []string, h hunk, bHadNL, aHadNL bool) {
	bStart, bLen := -1, 0
	aStart, aLen := -1, 0
	for k := h.startOp; k < h.endOp; k++ {
		o := ops[k]
		if o.kind != '+' {
			if bStart == -1 {
				bStart = o.bIdx
			}
			bLen++
		}
		if o.kind != '-' {
			if aStart == -1 {
				aStart = o.aIdx
			}
			aLen++
		}
	}
	hBStart := bStart + 1
	hAStart := aStart + 1
	if bLen == 0 {
		hBStart = 0
	}
	if aLen == 0 {
		hAStart = 0
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", hBStart, bLen, hAStart, aLen)
	for k := h.startOp; k < h.endOp; k++ {
		o := ops[k]
		switch o.kind {
		case ' ':
			line := before[o.bIdx]
			fmt.Fprintf(out, " %s\n", line)
			last := k == len(ops)-1
			if last {
				if o.bIdx == len(before)-1 && !bHadNL {
					out.WriteString("\\ No newline at end of file\n")
				}
				if o.aIdx == len(after)-1 && !aHadNL {
					out.WriteString("\\ No newline at end of file\n")
				}
			}
		case '-':
			fmt.Fprintf(out, "-%s\n", before[o.bIdx])
			if o.bIdx == len(before)-1 && !bHadNL && !hasLaterBefore(ops, k) {
				out.WriteString("\\ No newline at end of file\n")
			}
		case '+':
			fmt.Fprintf(out, "+%s\n", after[o.aIdx])
			if o.aIdx == len(after)-1 && !aHadNL && !hasLaterAfter(ops, k) {
				out.WriteString("\\ No newline at end of file\n")
			}
		}
	}
}

func hasLaterBefore(ops []diffOp, i int) bool {
	for k := i + 1; k < len(ops); k++ {
		if ops[k].kind != '+' {
			return true
		}
	}
	return false
}

func hasLaterAfter(ops []diffOp, i int) bool {
	for k := i + 1; k < len(ops); k++ {
		if ops[k].kind != '-' {
			return true
		}
	}
	return false
}
