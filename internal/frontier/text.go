package frontier

import "idiom/internal/frontier/pathparse"

func LooksLikeWorkspacePathToken(candidate string) bool {
	return pathparse.LooksLikeWorkspacePathToken(candidate)
}

func HasWorkspaceFileExtension(candidate string) bool {
	return pathparse.HasWorkspaceFileExtension(candidate)
}

func PromptMentionedWorkspacePaths(prompt string) []string {
	return pathparse.MentionedWorkspacePaths(prompt)
}

func PromptWorkspacePathFieldCandidates(prompt string) []string {
	return pathparse.WorkspacePathFieldCandidates(prompt)
}

func NormalizePromptWorkspacePathToken(token string) (string, bool) {
	return pathparse.NormalizeWorkspacePathToken(token)
}

func StripPromptPathLocationSuffix(candidate string) string {
	return pathparse.StripPathLocationSuffix(candidate)
}

func AllASCIIDigits(value string) bool {
	return pathparse.AllASCIIDigits(value)
}

func QuotedPromptFragments(prompt string) []string {
	return pathparse.QuotedFragments(prompt)
}

func PrimaryLocateLiteral(prompt string) string {
	return pathparse.PrimaryLocateLiteral(prompt)
}

func TrimLocateLiteralTail(text string) string {
	return pathparse.TrimLocateLiteralTail(text)
}
