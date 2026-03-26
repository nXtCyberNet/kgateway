package scenarios

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GetStreamingScenario returns the S4 (SSE Streaming) configuration.
func GetStreamingScenario() *Scenario {
	return &Scenario{
		Name:                   "S4-Streaming",
		Description:            "Inference routing for SSE streaming requests",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
		TargetRPS:              30,
		DurationSeconds:        180,
		ConcurrentUsers:        5,
		WarmupSeconds:          30,
		BackendTiers: []BackendTier{
			{
				Name:            "tier-large",
				CPULimit:        "4",
				MemoryLimit:     "4Gi",
				ResponseDelayMs: 50,
				Replicas:        2,
				Labels:          map[string]string{"tier": "large", "app": "llm-d-sim"},
			},
		},
	}
}

// MeasureStreamingMetrics sends a single SSE request and calculates TTFT and ITL.
// TTFT: Time to first data: chunk.
// ITL: Inter-Token Latency (mean time between subsequent chunks).
func MeasureStreamingMetrics(ctx context.Context, url, model string) (ttftMs float64, itlMeanUs float64, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(fmt.Sprintf(`{"model": "%s", "stream": true}`, model)))
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var firstChunkReceived bool
	var lastChunkTime time.Time
	var deltas []time.Duration

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		now := time.Now()
		if !firstChunkReceived {
			ttftMs = float64(now.Sub(start).Milliseconds())
			firstChunkReceived = true
		} else {
			deltas = append(deltas, now.Sub(lastChunkTime))
		}
		lastChunkTime = now
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scanner error: %w", err)
	}

	if len(deltas) > 0 {
		var total time.Duration
		for _, d := range deltas {
			total += d
		}
		itlMeanUs = float64(total.Microseconds()) / float64(len(deltas))
	}

	return ttftMs, itlMeanUs, nil
}
