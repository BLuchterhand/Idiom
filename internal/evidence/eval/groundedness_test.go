package eval

import "testing"

func TestGroundednessScoreReturnsFractionOfMatchedTokens(t *testing.T) {
	context := "The runner enforces the request budget through the budget ledger snapshot."
	high := "The runner enforces the request budget through the budget ledger."
	low := "Generally speaking, the answer depends on context."

	highScore := GroundednessScore(high, context)
	lowScore := GroundednessScore(low, context)
	if highScore <= lowScore {
		t.Fatalf("expected grounded > generic; got high=%v low=%v", highScore, lowScore)
	}
	if highScore < 0.5 {
		t.Fatalf("grounded response should clear 0.5, got %v", highScore)
	}
}

func TestGroundednessScoreEmptyInputs(t *testing.T) {
	if GroundednessScore("", "anything") != 0 {
		t.Fatalf("empty response must score 0")
	}
	if GroundednessScore("anything", "") != 0 {
		t.Fatalf("empty context must score 0")
	}
}

func TestDetectGenericPhrasesFlagsKnownLeaks(t *testing.T) {
	text := "However, based on my knowledge, the runner generally speaking handles retries."
	hits := DetectGenericPhrases(text)
	if len(hits) == 0 {
		t.Fatalf("expected at least one generic phrase, got none")
	}
}

func TestDetectGenericPhrasesIgnoresGroundedAnswers(t *testing.T) {
	text := "runner.go retries via the request budget ledger when the agent rejects a tool result."
	if hits := DetectGenericPhrases(text); len(hits) != 0 {
		t.Fatalf("grounded answer flagged hits: %v", hits)
	}
}

func TestGenericPhraseBandClassifies(t *testing.T) {
	if GenericPhraseBand(0.80, 0.75, 0.40) != "boost" {
		t.Fatalf("0.80 should land in boost band")
	}
	if GenericPhraseBand(0.30, 0.75, 0.40) != "penalize" {
		t.Fatalf("0.30 should land in penalize band")
	}
	if GenericPhraseBand(0.55, 0.75, 0.40) != "no_op" {
		t.Fatalf("0.55 should land in no_op band")
	}
}
