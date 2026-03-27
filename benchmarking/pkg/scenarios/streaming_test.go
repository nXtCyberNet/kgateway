// pkg/scenarios/streaming_test.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── SSE test server helpers ───────────────────────────────────────────────────

// sseServer starts a test HTTP server that returns well-formed SSE chunks.
// Each call to the handler emits `chunks` data: lines then a [DONE] sentinel.
func sseServer(t *testing.T, chunks int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			payload := map[string]interface{}{
				"id":      i,
				"choices": []map[string]interface{}{{"text": fmt.Sprintf("token%d", i)}},
			}
			b, _ := json.Marshal(payload)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond) // tiny gap so ITL is measurable
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

// emptySSEServer returns a server that sends no data: lines (only [DONE]).
func emptySSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
}

// errorServer returns a non-200 status code.
func errorServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
}

// ── MeasureStreamingMetrics ───────────────────────────────────────────────────

func TestMeasureStreamingMetrics_SingleChunk(t *testing.T) {
	srv := sseServer(t, 1)
	defer srv.Close()

	result, err := MeasureStreamingMetrics(context.Background(), srv.URL, "test-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TTFTMs <= 0 {
		t.Errorf("TTFT should be positive, got %.2f ms", result.TTFTMs)
	}
	// Single chunk: no ITL calculable — should be 0.
	if result.ITLMeanUs != 0 {
		t.Errorf("ITL with 1 chunk should be 0, got %.2f μs", result.ITLMeanUs)
	}
}

func TestMeasureStreamingMetrics_MultipleChunks(t *testing.T) {
	srv := sseServer(t, 5)
	defer srv.Close()

	result, err := MeasureStreamingMetrics(context.Background(), srv.URL, "test-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TTFTMs <= 0 {
		t.Errorf("TTFT should be positive, got %.2f ms", result.TTFTMs)
	}
	if result.ITLMeanUs <= 0 {
		t.Errorf("ITL with 5 chunks should be positive, got %.2f μs", result.ITLMeanUs)
	}
}

func TestMeasureStreamingMetrics_EmptyStream(t *testing.T) {
	srv := emptySSEServer(t)
	defer srv.Close()

	_, err := MeasureStreamingMetrics(context.Background(), srv.URL, "test-model")
	if err == nil {
		t.Error("expected error when no data: chunks received")
	}
	if !strings.Contains(err.Error(), "no data: chunks received") {
		t.Errorf("error should mention missing chunks, got: %v", err)
	}
}

func TestMeasureStreamingMetrics_Non200Status(t *testing.T) {
	srv := errorServer(t, http.StatusServiceUnavailable)
	defer srv.Close()

	_, err := MeasureStreamingMetrics(context.Background(), srv.URL, "test-model")
	if err == nil {
		t.Error("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention status code 503, got: %v", err)
	}
}

func TestMeasureStreamingMetrics_ContextCancelled(t *testing.T) {
	// The server writes headers but then stalls — the short-timeout context on
	// the client side should unblock the call.  We must NOT block the handler
	// goroutine on the request context because httptest.Server.Close() will
	// block waiting for open connections to drain, creating a deadlock.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write SSE headers but don't send any data or close the body so the
		// client read call blocks. Use a short sleep so the handler exits
		// promptly after the client cancels, allowing the server to close.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Wait at most 500ms for the request context to be cancelled by the
		// client, then return so httptest.Server.Close() can drain cleanly.
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	_, err := MeasureStreamingMetrics(ctx, srv.URL, "test-model")
	if err == nil {
		t.Error("expected error when context times out")
	}
}

func TestMeasureStreamingMetrics_PathAppendedOnce(t *testing.T) {
	// Verify that /v1/completions is not double-appended.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	MeasureStreamingMetrics(context.Background(), srv.URL, "m")
	if gotPath != "/v1/completions" {
		t.Errorf("path: got %q, want /v1/completions", gotPath)
	}
}

func TestMeasureStreamingMetrics_TrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	// baseURL with trailing slash — should still produce exactly /v1/completions
	MeasureStreamingMetrics(context.Background(), srv.URL+"/", "m")
	if gotPath != "/v1/completions" {
		t.Errorf("path with trailing slash: got %q, want /v1/completions", gotPath)
	}
}

// ── MeasureStreamingMetricsSampled ───────────────────────────────────────────

func TestMeasureStreamingMetricsSampled_AveragesResults(t *testing.T) {
	srv := sseServer(t, 4)
	defer srv.Close()

	ttft, itl, err := MeasureStreamingMetricsSampled(context.Background(), srv.URL, "m", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ttft <= 0 {
		t.Errorf("mean TTFT should be positive, got %.2f", ttft)
	}
	if itl <= 0 {
		t.Errorf("mean ITL should be positive, got %.2f", itl)
	}
}

func TestMeasureStreamingMetricsSampled_ContextCancelledMidway(t *testing.T) {
	srv := sseServer(t, 2)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so no samples can complete.
	cancel()

	_, _, err := MeasureStreamingMetricsSampled(ctx, srv.URL, "m", 5)
	if err == nil {
		t.Error("expected error when context is already cancelled")
	}
}

func TestMeasureStreamingMetricsSampled_ZeroSamples(t *testing.T) {
	srv := emptySSEServer(t)
	defer srv.Close()

	// All samples will fail, expecting an error
	_, _, err := MeasureStreamingMetricsSampled(context.Background(), srv.URL, "m", 3)
	if err == nil {
		t.Error("expected error when all samples fail")
	}
}

// ── calculateMeanITL ─────────────────────────────────────────────────────────

func TestCalculateMeanITL_ZeroChunks(t *testing.T) {
	if itl := calculateMeanITL([]time.Time{}); itl != 0 {
		t.Errorf("empty slice: got %.2f, want 0", itl)
	}
}

func TestCalculateMeanITL_OneChunk(t *testing.T) {
	if itl := calculateMeanITL([]time.Time{time.Now()}); itl != 0 {
		t.Errorf("single chunk: got %.2f, want 0", itl)
	}
}

func TestCalculateMeanITL_TwoChunks(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(10 * time.Millisecond)
	itl := calculateMeanITL([]time.Time{t0, t1})
	// 10ms = 10_000μs
	if itl < 9000 || itl > 11000 {
		t.Errorf("two chunks 10ms apart: ITL %.2f μs not in [9000,11000]", itl)
	}
}

func TestCalculateMeanITL_UniformSpacing(t *testing.T) {
	t0 := time.Now()
	timestamps := []time.Time{t0}
	for i := 1; i <= 5; i++ {
		timestamps = append(timestamps, t0.Add(time.Duration(i)*10*time.Millisecond))
	}
	itl := calculateMeanITL(timestamps)
	// Each gap is 10ms = 10_000μs
	if itl < 9000 || itl > 11000 {
		t.Errorf("uniform 10ms spacing: ITL %.2f μs not in [9000,11000]", itl)
	}
}

// ── createStreamingRequest ────────────────────────────────────────────────────

func TestCreateStreamingRequest_Headers(t *testing.T) {
	req, err := createStreamingRequest(context.Background(), "http://localhost:8080", "llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	if accept := req.Header.Get("Accept"); accept != "text/event-stream" {
		t.Errorf("Accept: got %q, want text/event-stream", accept)
	}
}

func TestCreateStreamingRequest_Method(t *testing.T) {
	req, err := createStreamingRequest(context.Background(), "http://localhost:8080", "m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("method: got %q, want POST", req.Method)
	}
}

func TestCreateStreamingRequest_BodyContainsStream(t *testing.T) {
	req, err := createStreamingRequest(context.Background(), "http://localhost:8080", "llama3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	streamVal, ok := body["stream"]
	if !ok {
		t.Fatal("body missing 'stream' field")
	}
	if streamVal != true {
		t.Errorf("stream: got %v, want true", streamVal)
	}
}

func TestCreateStreamingRequest_ModelInBody(t *testing.T) {
	req, err := createStreamingRequest(context.Background(), "http://localhost:8080", "meta-llama/Llama-3-8b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["model"] != "meta-llama/Llama-3-8b" {
		t.Errorf("model: got %v, want meta-llama/Llama-3-8b", body["model"])
	}
}
