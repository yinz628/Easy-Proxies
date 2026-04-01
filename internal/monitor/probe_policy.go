package monitor

import (
	"fmt"
	"time"
)

const MaxAcceptedProbeLatency = 10 * time.Second

func ProbeLatencyError(latency time.Duration) error {
	if latency <= 0 || latency <= MaxAcceptedProbeLatency {
		return nil
	}
	return fmt.Errorf("probe exceeded %dms", MaxAcceptedProbeLatency.Milliseconds())
}
