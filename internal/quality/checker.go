package quality

import "time"

const (
	StatusPass = "pass"
	StatusWarn = "warn"
	StatusFail = "fail"

	StatusDualAvailable = "dual_available"
	StatusOpenAIOnly    = "openai_only"
	StatusAnthropicOnly = "anthropic_only"
	StatusUnavailable   = "unavailable"

	TargetOpenAIReachability    = "openai_reachability"
	TargetAnthropicReachability = "anthropic_reachability"

	QualityVersionAIReachabilityV2 = "ai_reachability_v2"
)

type CheckItem struct {
	Target     string `json:"target"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Message    string `json:"message,omitempty"`
}

type CheckResult struct {
	BaseLatencyMs   int64       `json:"base_latency_ms,omitempty"`
	ExitIP          string      `json:"exit_ip,omitempty"`
	Country         string      `json:"country,omitempty"`
	CountryCode     string      `json:"country_code,omitempty"`
	Region          string      `json:"region,omitempty"`
	OpenAIStatus    string      `json:"quality_openai_status"`
	AnthropicStatus string      `json:"quality_anthropic_status"`
	QualityVersion  string      `json:"quality_version"`
	Score           int         `json:"score"`
	Grade           string      `json:"grade"`
	Status          string      `json:"status"`
	Summary         string      `json:"summary"`
	CheckedAt       time.Time   `json:"checked_at"`
	Items           []CheckItem `json:"items"`
}

func FinalizeResult(result *CheckResult) {
	if result == nil {
		return
	}

	result.OpenAIStatus = normalizeProviderStatus(itemStatus(result.Items, TargetOpenAIReachability))
	result.AnthropicStatus = normalizeProviderStatus(itemStatus(result.Items, TargetAnthropicReachability))
	result.QualityVersion = QualityVersionAIReachabilityV2

	switch {
	case result.OpenAIStatus == StatusPass && result.AnthropicStatus == StatusPass:
		result.Status = StatusDualAvailable
		result.Score = 100
		result.Grade = "A"
		result.Summary = "OpenAI 可用，Anthropic 可用"
	case result.OpenAIStatus == StatusPass && result.AnthropicStatus == StatusFail:
		result.Status = StatusOpenAIOnly
		result.Score = 70
		result.Grade = "B"
		result.Summary = "OpenAI 可用，Anthropic 不可用"
	case result.OpenAIStatus == StatusFail && result.AnthropicStatus == StatusPass:
		result.Status = StatusAnthropicOnly
		result.Score = 30
		result.Grade = "C"
		result.Summary = "OpenAI 不可用，Anthropic 可用"
	default:
		result.Status = StatusUnavailable
		result.Score = 0
		result.Grade = "F"
		result.Summary = "OpenAI 不可用，Anthropic 不可用"
	}

	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now()
	}
}

func itemStatus(items []CheckItem, target string) string {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Target == target {
			return items[i].Status
		}
	}
	return StatusFail
}

func normalizeProviderStatus(status string) string {
	if status == StatusPass {
		return StatusPass
	}
	return StatusFail
}
