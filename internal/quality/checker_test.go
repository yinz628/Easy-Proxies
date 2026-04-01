package quality

import "testing"

func TestFinalizeResultComputesScoreAndGrade(t *testing.T) {
	result := &CheckResult{
		Items: []CheckItem{
			{Target: "base_connectivity", Status: StatusPass},
			{Target: "openai", Status: StatusWarn},
			{Target: "anthropic", Status: StatusFail},
			{Target: "gemini", Status: StatusPass},
		},
	}

	FinalizeResult(result)

	if got, want := result.Score, 65; got != want {
		t.Fatalf("Score = %d, want %d", got, want)
	}
	if got, want := result.Grade, "C"; got != want {
		t.Fatalf("Grade = %q, want %q", got, want)
	}
	if got, want := result.Status, StatusFailed; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
}

func TestOverallStatusPrefersFailedOverWarn(t *testing.T) {
	result := &CheckResult{
		Items: []CheckItem{
			{Target: "base_connectivity", Status: StatusPass},
			{Target: "openai", Status: StatusWarn},
			{Target: "anthropic", Status: StatusFail},
		},
	}

	FinalizeResult(result)

	if got, want := result.Status, StatusFailed; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
}
