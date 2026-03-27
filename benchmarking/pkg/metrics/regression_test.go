// pkg/metrics/regression_test.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func baselineResult(p99 float64) *scenarios.Results {
	return &scenarios.Results{
		ScenarioName:  "baseline",
		DataPlane:     "envoy",
		Timestamp:     time.Now(),
		P99LatencyMs:  p99,
		ErrorRate:     0.001,
		ThroughputRPS: 100,
	}
}

func currentResult(scenario string, p99, errRate float64) *scenarios.Results {
	return &scenarios.Results{
		ScenarioName: scenario,
		DataPlane:    "envoy",
		Timestamp:    time.Now(),
		P99LatencyMs: p99,
		ErrorRate:    errRate,
	}
}

// ── CheckRegression ──────────────────────────────────────────────────────────

func TestCheckRegression_NilBaseline(t *testing.T) {
	cur := currentResult("inference-routing", 15.0, 0.001)
	reg := CheckRegression(cur, nil, 20.0)
	if reg != nil {
		t.Errorf("nil baseline should return nil regression, got: %+v", reg)
	}
}

func TestCheckRegression_NoRegressionLatency(t *testing.T) {
	base := baselineResult(10.0)
	cur := currentResult("inference-routing", 11.0, 0.001) // +10%, threshold 20%
	reg := CheckRegression(cur, base, 20.0)
	if reg == nil {
		t.Fatal("expected a non-nil regression result")
	}
	if reg.Exceeded {
		t.Errorf("10%% increase should not exceed 20%% threshold, DeltaPct=%.2f", reg.DeltaPct)
	}
}

func TestCheckRegression_LatencyRegression(t *testing.T) {
	base := baselineResult(10.0)
	cur := currentResult("inference-routing", 14.0, 0.001) // +40%
	reg := CheckRegression(cur, base, 20.0)
	if reg == nil {
		t.Fatal("expected non-nil regression result")
	}
	if !reg.Exceeded {
		t.Errorf("40%% increase should exceed 20%% threshold")
	}
}

func TestCheckRegression_ExactThreshold(t *testing.T) {
	// Exactly at the boundary: 10 * 1.20 = 12.0 — NOT exceeded (> not >=).
	base := baselineResult(10.0)
	cur := currentResult("scenario", 12.0, 0.001)
	reg := CheckRegression(cur, base, 20.0)
	if reg.Exceeded {
		t.Error("result exactly at threshold should NOT be flagged as regression")
	}
}

func TestCheckRegression_JustAboveThreshold(t *testing.T) {
	base := baselineResult(10.0)
	cur := currentResult("scenario", 12.01, 0.001) // just above 12.0
	reg := CheckRegression(cur, base, 20.0)
	if !reg.Exceeded {
		t.Error("result marginally above threshold should be flagged as regression")
	}
}

func TestCheckRegression_ErrorRateSilentFailure(t *testing.T) {
	// Latency is fine but error rate jumps by > 1%.
	base := baselineResult(10.0)
	cur := &scenarios.Results{
		ScenarioName: "scenario",
		P99LatencyMs: 10.5,
		ErrorRate:    0.02, // baseline was 0.001, jump > 0.01
	}
	reg := CheckRegression(cur, base, 20.0)
	if !reg.Exceeded {
		t.Error("error rate jump > 1%% should flag regression via silent failure check")
	}
}

func TestCheckRegression_DeltaPctCalculation(t *testing.T) {
	base := baselineResult(10.0)
	cur := currentResult("s", 12.0, 0.001) // +20%
	reg := CheckRegression(cur, base, 25.0)
	if reg.DeltaPct < 19.9 || reg.DeltaPct > 20.1 {
		t.Errorf("DeltaPct: expected ~20.0, got %.4f", reg.DeltaPct)
	}
}

func TestCheckRegression_ZeroBaselineP99(t *testing.T) {
	// Division by zero guard: if baseline P99 is 0, DeltaPct stays 0.
	base := baselineResult(0.0)
	cur := currentResult("s", 5.0, 0.001)
	reg := CheckRegression(cur, base, 20.0)
	if reg == nil {
		t.Fatal("expected non-nil result")
	}
	if reg.DeltaPct != 0 {
		t.Errorf("DeltaPct with 0 baseline should be 0, got %.2f", reg.DeltaPct)
	}
}

func TestCheckRegression_FieldsPopulated(t *testing.T) {
	base := baselineResult(10.0)
	cur := currentResult("my-scenario", 15.0, 0.002)
	reg := CheckRegression(cur, base, 20.0)
	if reg.ScenarioName != "my-scenario" {
		t.Errorf("ScenarioName: got %q, want my-scenario", reg.ScenarioName)
	}
	if reg.BaselineP99 != 10.0 {
		t.Errorf("BaselineP99: got %.2f, want 10.0", reg.BaselineP99)
	}
	if reg.CurrentP99 != 15.0 {
		t.Errorf("CurrentP99: got %.2f, want 15.0", reg.CurrentP99)
	}
	if reg.Threshold != 20.0 {
		t.Errorf("Threshold: got %.2f, want 20.0", reg.Threshold)
	}
}

// ── LoadBaseline ─────────────────────────────────────────────────────────────

func TestLoadBaseline_ValidJSON(t *testing.T) {
	res := baselineResult(12.5)
	data, _ := json.Marshal(res)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.P99LatencyMs != 12.5 {
		t.Errorf("P99: got %.2f, want 12.5", loaded.P99LatencyMs)
	}
	if loaded.ScenarioName != "baseline" {
		t.Errorf("ScenarioName: got %q, want baseline", loaded.ScenarioName)
	}
}

func TestLoadBaseline_FileNotFound(t *testing.T) {
	_, err := LoadBaseline(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadBaseline_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := LoadBaseline(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── SaveResults ──────────────────────────────────────────────────────────────

func TestSaveResults_WritesFile(t *testing.T) {
	dir := t.TempDir()
	res := &scenarios.Results{
		ScenarioName:  "baseline",
		DataPlane:     "envoy",
		Timestamp:     time.Now(),
		P99LatencyMs:  10.0,
		ThroughputRPS: 100,
	}
	path, err := SaveResults(res, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("result file not found at %s: %v", path, err)
	}
}

func TestSaveResults_FileNameContainsScenarioAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	res := &scenarios.Results{
		ScenarioName: "header-routing",
		DataPlane:    "envoy",
		Timestamp:    time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
	}
	path, err := SaveResults(res, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "header-routing_") {
		t.Errorf("filename should start with scenario name, got %q", base)
	}
	if !strings.Contains(base, "20260115") {
		t.Errorf("filename should contain date 20260115, got %q", base)
	}
}

func TestSaveResults_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	res := &scenarios.Results{
		ScenarioName:  "baseline",
		DataPlane:     "envoy",
		Timestamp:     time.Now(),
		P99LatencyMs:  9.8,
		ThroughputRPS: 92,
	}
	path, err := SaveResults(res, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result file: %v", err)
	}
	var loaded scenarios.Results
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("result file is not valid JSON: %v", err)
	}
	if loaded.P99LatencyMs != 9.8 {
		t.Errorf("P99: got %.2f, want 9.8", loaded.P99LatencyMs)
	}
}

func TestSaveResults_CreatesOutputDir(t *testing.T) {
	// Output dir does not exist yet — SaveResults should create it.
	dir := filepath.Join(t.TempDir(), "nested", "output")
	res := &scenarios.Results{
		ScenarioName: "baseline",
		DataPlane:    "envoy",
		Timestamp:    time.Now(),
	}
	if _, err := SaveResults(res, dir); err != nil {
		t.Errorf("SaveResults should create output dir, got: %v", err)
	}
}

// ── GenerateSummaryTable ─────────────────────────────────────────────────────

func TestGenerateSummaryTable_ContainsScenarioName(t *testing.T) {
	results := []*scenarios.Results{
		{ScenarioName: "baseline", DataPlane: "envoy", P99LatencyMs: 10.0, ThroughputRPS: 100},
		{ScenarioName: "inference-routing", DataPlane: "envoy", P99LatencyMs: 14.0, GatewayOverheadMs: 4.0, ThroughputRPS: 92},
	}
	table := GenerateSummaryTable(results, nil)
	if !strings.Contains(table, "baseline") {
		t.Error("table should contain 'baseline'")
	}
	if !strings.Contains(table, "inference-routing") {
		t.Error("table should contain 'inference-routing'")
	}
}

func TestGenerateSummaryTable_ContainsHeaders(t *testing.T) {
	results := []*scenarios.Results{
		{ScenarioName: "baseline", DataPlane: "envoy"},
	}
	table := GenerateSummaryTable(results, nil)
	for _, hdr := range []string{"Scenario", "DataPlane", "P99(ms)", "Overhead"} {
		if !strings.Contains(table, hdr) {
			t.Errorf("table should contain header %q", hdr)
		}
	}
}

func TestGenerateSummaryTable_OverheadDashForBaseline(t *testing.T) {
	results := []*scenarios.Results{
		{ScenarioName: "baseline", DataPlane: "envoy", GatewayOverheadMs: 0},
	}
	table := GenerateSummaryTable(results, nil)
	if !strings.Contains(table, "-") {
		t.Error("baseline overhead should display as '-'")
	}
}

func TestGenerateSummaryTable_EmptyResults(t *testing.T) {
	// Should not panic with empty slice.
	table := GenerateSummaryTable(nil, nil)
	if table == "" {
		t.Error("table should not be empty even with no results (header is always rendered)")
	}
}

func TestGenerateSummaryTable_OverheadShownForNonBaseline(t *testing.T) {
	results := []*scenarios.Results{
		{ScenarioName: "inference-routing", DataPlane: "envoy", GatewayOverheadMs: 4.5},
	}
	table := GenerateSummaryTable(results, nil)
	if !strings.Contains(table, "4.50") {
		t.Errorf("table should contain overhead value '4.50', got:\n%s", table)
	}
}
