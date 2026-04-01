package scenarios

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// S4Streaming returns the SSE streaming scenario. Lower RPS and longer duration give
// enough steady-state stream data for statistically meaningful TTFT and ITL measurements.
func S4Streaming() *Scenario {
	return &Scenario{
		Name:                   "streaming",
		Description:            "SSE streaming load — measures TTFT and inter-token latency under EPP routing",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
		TargetRPS:              30,
		DurationSeconds:        180,
		ConcurrentUsers:        10,
		WarmupSeconds:          60,
		BackendTiers: []BackendTier{
			{
				Name:                    "tier-large",
				CPULimit:                "4",
				MemoryLimit:             "4Gi",
				ResponseDelayMs:         50,
				Replicas:                2,
				Labels:                  map[string]string{"app": "llm-d-sim", "tier": "large"},
				SimulatedKVCachePercent: 20,
			},
		},
	}
}

// StreamingResult holds timing data for a single SSE request.
type StreamingResult struct {
	TTFTMs    float64
	ITLMeanUs float64
}

// MeasureStreamingMetrics sends a single SSE request to baseURL for the given model
// and records TTFT (time to first data: chunk) and mean ITL (mean delta between chunks).
// baseURL must be the base address only (e.g. "http://localhost:8080") — the
// /v1/completions path is appended internally.
func MeasureStreamingMetrics(ctx context.Context, baseURL, model string) (*StreamingResult, error) {
	req, err := createStreamingRequest(ctx, baseURL, model)
	if err != nil {
		return nil, fmt.Errorf("failed to build streaming request: %w", err)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streaming request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from streaming endpoint", resp.StatusCode)
	}

	ttftMs, itlMeanUs, err := consumeSSEStream(resp, start)
	if err != nil {
		return nil, fmt.Errorf("error consuming SSE stream: %w", err)
	}

	return &StreamingResult{TTFTMs: ttftMs, ITLMeanUs: itlMeanUs}, nil
}

// MeasureStreamingMetricsSampled runs MeasureStreamingMetrics n times and returns
// the mean TTFT and ITL across successful samples, reducing measurement noise.
func MeasureStreamingMetricsSampled(ctx context.Context, baseURL, model string, n int) (ttftMeanMs, itlMeanUs float64, err error) {
	var ttftSum, itlSum float64
	var successes int

	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return 0, 0, fmt.Errorf("sampling cancelled after %d samples: %w", i, ctx.Err())
		default:
		}

		result, sampleErr := MeasureStreamingMetrics(ctx, baseURL, model)
		if sampleErr != nil {
			continue
		}
		ttftSum += result.TTFTMs
		itlSum += result.ITLMeanUs
		successes++
	}

	if successes == 0 {
		return 0, 0, fmt.Errorf("all %d streaming samples failed", n)
	}

	return ttftSum / float64(successes), itlSum / float64(successes), nil
}

// createStreamingRequest builds an HTTP POST to baseURL/v1/completions with stream:true.
// Accept: text/event-stream tells the simulator to send SSE chunks rather than a
// single buffered JSON response.
func createStreamingRequest(ctx context.Context, baseURL, model string) (*http.Request, error) {
	body := map[string]interface{}{
		"model":      model,
		"prompt":     "Tell me about AI gateway routing.",
		"max_tokens": 100,
		"stream":     true,
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// baseURL must not already contain the path — append it here exactly once.
	endpoint := strings.TrimRight(baseURL, "/") + "/v1/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	return req, nil
}

// consumeSSEStream reads SSE chunks line by line. Records the time of the first
// data: chunk as TTFT and the mean delta between subsequent chunks as ITL.
func consumeSSEStream(resp *http.Response, reqStart time.Time) (ttftMs float64, itlMeanUs float64, err error) {
	var chunkTimestamps []time.Time

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(line, "data:")) == "[DONE]" {
			break
		}
		chunkTimestamps = append(chunkTimestamps, time.Now())
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("error reading SSE stream: %w", err)
	}

	if len(chunkTimestamps) == 0 {
		return 0, 0, fmt.Errorf("no data: chunks received — check Accept: text/event-stream header")
	}

	ttftMs = float64(chunkTimestamps[0].Sub(reqStart).Milliseconds())
	itlMeanUs = calculateMeanITL(chunkTimestamps)

	return ttftMs, itlMeanUs, nil
}

// calculateMeanITL returns the mean inter-token latency in microseconds.
// Returns 0 when fewer than 2 chunks were received.
func calculateMeanITL(timestamps []time.Time) float64 {
	if len(timestamps) < 2 {
		return 0
	}

	var totalUs float64
	for i := 1; i < len(timestamps); i++ {
		totalUs += float64(timestamps[i].Sub(timestamps[i-1]).Microseconds())
	}

	return totalUs / float64(len(timestamps)-1)
}
