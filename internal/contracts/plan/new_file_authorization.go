package plancontract

import "strings"

func NewFileAuthorizedForPath(contextText string, path string) bool {
	if ExplicitNewFileBodyContext(contextText) {
		return true
	}

	lower := strings.ToLower(contextText)

	return PathLooksLikeTest(path) &&
		strings.Contains(lower, "add") &&
		strings.Contains(lower, "test")
}

func ExplicitNewFileBodyContext(contextText string) bool {
	lower := strings.ToLower(contextText)

	return strings.Contains(lower, "create ") ||
		strings.Contains(lower, "creating ") ||
		(strings.Contains(lower, "add ") && strings.Contains(lower, " package")) ||
		strings.Contains(lower, "new package") ||
		strings.Contains(lower, "source package") ||
		strings.Contains(lower, "package plus test") ||
		strings.Contains(lower, "new file") ||
		strings.Contains(lower, "new repo-relative file") ||
		strings.Contains(lower, "full file body") ||
		strings.Contains(lower, "following content")
}
