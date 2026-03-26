package scenarios

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// S4Streaming returns Scenario 4 configuration: SSE testing.
func S4Streaming() *Scenario {
	return &Scenario{
		Name:                   "streaming",
		Description:            "SSE event stream test measuring TTFT and ITL across inference connections.",
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
				Labels:          map[string]string{"app": "llm-d-sim", "tier": "large"},
			},
		},
	}
}

// MeasureStreamingMetrics consumes an SSE endpoint to calculate TTFT and ITL
func MeasureStreamingMetrics(url, model string) (ttftMs float64, itlMeanUs float64, err error) {
	req, err := createStreamingRequest(url, model)
	if err != nil {
		return 0, 0, err
	}

	client := &http.Client{}
	start := time.Now()
	
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("do streaming req: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return consumeSSEStream(resp.Body, start)
}

// createStreamingRequest builds the HTTP POST request customized for SSE chunks
func createStreamingRequest(url, model string) (*http.Request, error) {
	reqBody := []byte(fmt.Sprintf(`{"model":"%s","stream":true,"messages":[{"role":"user","content":"hello"}]}`, model))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create streaming req: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

// consumeSSEStream reads the response body line by line recording the deltas of chunk arrival intervals
func consumeSSEStream(body io.Reader, startTime time.Time) (ttftMs float64, itlMeanUs float64, err error) {
	scanner := bufio.NewScanner(body)
	var chunkTimes []time.Time

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		msg := strings.TrimPrefix(line, "data: ")
		if msg == "[DONE]" {
			break
		}

		// parse chunk out briefly to simulate proper payload verification
		var dummy map[string]interface{}
		_ = json.Unmarshal([]byte(msg), &dummy)

		now := time.Now()
		if len(chunkTimes) == 0 {
			ttftMs = float64(now.Sub(startTime).Milliseconds())
		}
		chunkTimes = append(chunkTimes, now)
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan sse line: %w", err)
	}

	itlMeanUs = calculateMeanITL(chunkTimes)
	return ttftMs, itlMeanUs, nil
}

// calculateMeanITL finds the average delta (Inter-Token Latency) among all sequential timestamps
func calculateMeanITL(chunkTimes []time.Time) float64 {
	if len(chunkTimes) <= 1 {
		return 0
	}

	var totalDelta time.Duration
	for i := 1; i < len(chunkTimes); i++ {
		totalDelta += chunkTimes[i].Sub(chunkTimes[i-1])
	}
	
	return float64(totalDelta.Microseconds()) / float64(len(chunkTimes)-1)
}
