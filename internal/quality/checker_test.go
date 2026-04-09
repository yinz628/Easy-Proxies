package quality

import "testing"

func TestFinalizeResultAIReachabilityMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		openAIStatus    string
		anthropicStatus string
		wantStatus      string
		wantScore       int
		wantGrade       string
		wantSummary     string
	}{
		{
			name:            "dual_available",
			openAIStatus:    StatusPass,
			anthropicStatus: StatusPass,
			wantStatus:      StatusDualAvailable,
			wantScore:       100,
			wantGrade:       "A",
			wantSummary:     "OpenAI 可用，Anthropic 可用",
		},
		{
			name:            "openai_only",
			openAIStatus:    StatusPass,
			anthropicStatus: StatusFail,
			wantStatus:      StatusOpenAIOnly,
			wantScore:       70,
			wantGrade:       "B",
			wantSummary:     "OpenAI 可用，Anthropic 不可用",
		},
		{
			name:            "anthropic_only",
			openAIStatus:    StatusFail,
			anthropicStatus: StatusPass,
			wantStatus:      StatusAnthropicOnly,
			wantScore:       30,
			wantGrade:       "C",
			wantSummary:     "OpenAI 不可用，Anthropic 可用",
		},
		{
			name:            "unavailable",
			openAIStatus:    StatusFail,
			anthropicStatus: StatusFail,
			wantStatus:      StatusUnavailable,
			wantScore:       0,
			wantGrade:       "F",
			wantSummary:     "OpenAI 不可用，Anthropic 不可用",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := &CheckResult{
				Items: []CheckItem{
					{Target: TargetOpenAIReachability, Status: tc.openAIStatus},
					{Target: TargetAnthropicReachability, Status: tc.anthropicStatus},
				},
			}

			FinalizeResult(result)

			if got, want := result.OpenAIStatus, tc.openAIStatus; got != want {
				t.Fatalf("OpenAIStatus = %q, want %q", got, want)
			}
			if got, want := result.AnthropicStatus, tc.anthropicStatus; got != want {
				t.Fatalf("AnthropicStatus = %q, want %q", got, want)
			}
			if got, want := result.QualityVersion, QualityVersionAIReachabilityV2; got != want {
				t.Fatalf("QualityVersion = %q, want %q", got, want)
			}
			if got, want := result.Status, tc.wantStatus; got != want {
				t.Fatalf("Status = %q, want %q", got, want)
			}
			if got, want := result.Score, tc.wantScore; got != want {
				t.Fatalf("Score = %d, want %d", got, want)
			}
			if got, want := result.Grade, tc.wantGrade; got != want {
				t.Fatalf("Grade = %q, want %q", got, want)
			}
			if got, want := result.Summary, tc.wantSummary; got != want {
				t.Fatalf("Summary = %q, want %q", got, want)
			}
		})
	}
}

func TestFinalizeResultMissingAnthropicItemFallsBackToOpenAIOnly(t *testing.T) {
	t.Parallel()

	result := &CheckResult{
		Items: []CheckItem{
			{Target: TargetOpenAIReachability, Status: StatusPass},
		},
	}

	FinalizeResult(result)

	if got, want := result.OpenAIStatus, StatusPass; got != want {
		t.Fatalf("OpenAIStatus = %q, want %q", got, want)
	}
	if got, want := result.AnthropicStatus, StatusFail; got != want {
		t.Fatalf("AnthropicStatus = %q, want %q", got, want)
	}
	if got, want := result.Status, StatusOpenAIOnly; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
	if got, want := result.Score, 70; got != want {
		t.Fatalf("Score = %d, want %d", got, want)
	}
	if got, want := result.Grade, "B"; got != want {
		t.Fatalf("Grade = %q, want %q", got, want)
	}
	if got, want := result.Summary, "OpenAI 可用，Anthropic 不可用"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
	if got, want := result.QualityVersion, QualityVersionAIReachabilityV2; got != want {
		t.Fatalf("QualityVersion = %q, want %q", got, want)
	}
}
