package quality

import (
	"fmt"
	"time"
)

const (
	StatusPass    = "pass"
	StatusWarn    = "warn"
	StatusFail    = "fail"
	StatusHealthy = "healthy"
	StatusFailed  = "failed"
)

type CheckItem struct {
	Target     string `json:"target"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMs  int64  `json:"latency_ms,omitempty"`
	Message    string `json:"message,omitempty"`
}

type CheckResult struct {
	BaseLatencyMs int64       `json:"base_latency_ms,omitempty"`
	ExitIP        string      `json:"exit_ip,omitempty"`
	Country       string      `json:"country,omitempty"`
	CountryCode   string      `json:"country_code,omitempty"`
	Region        string      `json:"region,omitempty"`
	Score         int         `json:"score"`
	Grade         string      `json:"grade"`
	Status        string      `json:"status"`
	Summary       string      `json:"summary"`
	CheckedAt     time.Time   `json:"checked_at"`
	Items         []CheckItem `json:"items"`
}

func FinalizeResult(result *CheckResult) {
	if result == nil {
		return
	}

	passedCount := 0
	warnCount := 0
	failedCount := 0
	for _, item := range result.Items {
		switch item.Status {
		case StatusPass:
			passedCount++
		case StatusWarn:
			warnCount++
		default:
			failedCount++
		}
	}

	score := 100 - warnCount*10 - failedCount*25
	if failedCount > 0 {
		if score < 0 {
			score = 0
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	result.Score = score
	result.Grade = gradeForScore(score)
	result.Status = overallStatus(failedCount, warnCount, passedCount)
	result.Summary = fmt.Sprintf("通过 %d 项，告警 %d 项，失败 %d 项", passedCount, warnCount, failedCount)
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now()
	}
}

func gradeForScore(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}

func overallStatus(failedCount, warnCount, passedCount int) string {
	if failedCount > 0 {
		return StatusFailed
	}
	if warnCount > 0 {
		return StatusWarn
	}
	if passedCount > 0 {
		return StatusHealthy
	}
	return StatusFailed
}
