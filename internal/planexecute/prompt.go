package planexecute

import "idiom/internal/plancontract"

func RequiredEditPathsFromPrompt(prompt string) []string {
	return requiredEditPathsFromPrompt(prompt)
}

func AllowedEditPathsFromPrompt(prompt string) []string {
	return allowedEditPathsFromPrompt(prompt)
}

func requiredEditPathsFromPrompt(prompt string) []string {
	return plancontract.RequiredEditPathsFromPrompt(prompt)
}

func allowedEditPathsFromPrompt(prompt string) []string {
	return plancontract.AllowedEditPathsFromPrompt(prompt)
}
