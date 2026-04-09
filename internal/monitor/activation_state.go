package monitor

import (
	"strings"

	"easy_proxies/internal/quality"
	"easy_proxies/internal/store"
)

const (
	activationReadyReady   = "ready"
	activationReadyBlocked = "blocked"
)

func deriveActivationState(manualProbeStatus, openAIStatus, anthropicStatus string) (bool, string) {
	switch normalizeManualProbeStatus(manualProbeStatus) {
	case store.ManualProbeStatusPass:
		if normalizeProviderActivationStatus(openAIStatus) == quality.StatusPass ||
			normalizeProviderActivationStatus(anthropicStatus) == quality.StatusPass {
			return true, ""
		}
		return false, activationBlockedByQuality(openAIStatus, anthropicStatus)
	case store.ManualProbeStatusFail:
		return false, "入池预检失败"
	case store.ManualProbeStatusTimeout:
		return false, "入池预检超时"
	default:
		return false, "未完成入池预检"
	}
}

func activationReadyFacetValue(ready bool) string {
	if ready {
		return activationReadyReady
	}
	return activationReadyBlocked
}

func applyActivationStateToQualityCheck(check *store.NodeQualityCheck, manualProbe *store.NodeManualProbeResult) {
	if check == nil {
		return
	}

	manualProbeStatus := store.ManualProbeStatusUntested
	if manualProbe != nil {
		manualProbeStatus = manualProbe.Status
	}
	check.ActivationReady, check.ActivationBlockReason = deriveActivationState(
		manualProbeStatus,
		check.QualityOpenAIStatus,
		check.QualityAnthropicStatus,
	)
}

func normalizeManualProbeStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case store.ManualProbeStatusPass:
		return store.ManualProbeStatusPass
	case store.ManualProbeStatusFail:
		return store.ManualProbeStatusFail
	case store.ManualProbeStatusTimeout:
		return store.ManualProbeStatusTimeout
	default:
		return store.ManualProbeStatusUntested
	}
}

func normalizeProviderActivationStatus(status string) string {
	if strings.TrimSpace(strings.ToLower(status)) == quality.StatusPass {
		return quality.StatusPass
	}
	return quality.StatusFail
}

func activationBlockedByQuality(openAIStatus, anthropicStatus string) string {
	openAI := normalizeProviderActivationStatus(openAIStatus)
	anthropic := normalizeProviderActivationStatus(anthropicStatus)
	if openAI == quality.StatusFail && anthropic == quality.StatusFail {
		if strings.TrimSpace(openAIStatus) == "" && strings.TrimSpace(anthropicStatus) == "" {
			return "未完成质量检测"
		}
		return "OpenAI 和 Anthropic 均不可用"
	}
	return "质量检测未通过"
}
