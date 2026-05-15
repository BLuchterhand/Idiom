package util

import "strings"

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func CombineOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout == "" {
		return stderr
	}
	if stderr == "" {
		return stdout
	}
	return stdout + "\n" + stderr
}

func EqualTrimFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
