package agent

import "idiom/internal/runtime/agent/modepolicy"

type Mode = modepolicy.Mode

func BuildMode() Mode {
	return modepolicy.BuildMode()
}

func PlanMode() Mode {
	return modepolicy.PlanMode()
}

func GeneralMode() Mode {
	return modepolicy.GeneralMode()
}

func DefaultModes() []Mode {
	return modepolicy.DefaultModes()
}

func ModeByName(name string) (Mode, bool) {
	return modepolicy.ModeByName(name)
}

func ModeOrDefault(name string) Mode {
	return modepolicy.ModeOrDefault(name)
}

func ModeNames() []string {
	return modepolicy.ModeNames()
}

// ModePolicy returns tool-economy nudge defaults for the given Mode. These
// are soft budgets -- they inject a one-shot system message when crossed,
// never hard-stop a turn.
func ModePolicy(_ Mode) RunPolicy {
	return RunPolicy{
		FingerprintNudgeAt: 3,
		SynthesisNudgeAt:   10,
	}
}
