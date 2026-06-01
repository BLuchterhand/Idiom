package answergrounding

import "strings"

type TargetNormalizer struct{}

func (TargetNormalizer) Normalize(target string) string {
	target = strings.TrimSpace(target)
	target = strings.Trim(target, "`'\".,;:()[]")
	target = strings.ReplaceAll(target, "\\", "/")
	for strings.HasPrefix(target, "./") {
		target = strings.TrimPrefix(target, "./")
	}
	return target
}

func (n TargetNormalizer) Same(receiptTarget string, claimTarget string) bool {
	return n.Normalize(receiptTarget) == n.Normalize(claimTarget)
}

func sameTarget(receiptTarget string, claimTarget string) bool {
	return TargetNormalizer{}.Same(receiptTarget, claimTarget)
}

func normalizeTarget(target string) string {
	return TargetNormalizer{}.Normalize(target)
}
