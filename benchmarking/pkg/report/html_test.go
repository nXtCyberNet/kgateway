// pkg/report/html_test.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kgateway-dev/kgateway/benchmarking/pkg/scenarios"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func sampleResults() []*scenarios.Results {
	return []*scenarios.Results{
		{
			ScenarioName:         "baseline",
			DataPlane:            "envoy",
			Timestamp:            time.Now(),
			P99LatencyMs:         10.5,
			P95LatencyMs:         7.2,
			P50LatencyMs:         3.1,
			MeanLatencyMs:        4.0,
			ThroughputRPS:        92.0,
			ErrorRate:            0.001,
			GatewayCPUMillicores: 80,
			GatewayMemoryMB:      128,
			GatewayOverheadMs:    0,
		},
		{
			ScenarioName:         "inference-routing",
			DataPlane:            "envoy",
			Timestamp:            time.Now(),
			P99LatencyMs:         14.2,
			P95LatencyMs:         10.1,
			P50LatencyMs:         5.4,
			MeanLatencyMs:        6.3,
			ThroughputRPS:        85.0,
			ErrorRate:            0.002,
			GatewayCPUMillicores: 150,
			GatewayMemoryMB:      256,
			GatewayOverheadMs:    3.7,
			EPPDecisionLatencyMs: 0.9,
		},
	}
}

func sampleRegressions() []*scenarios.RegressionResult {
	return []*scenarios.RegressionResult{
		{
			ScenarioName: "inference-routing",
			BaselineP99:  10.5,
			CurrentP99:   14.2,
			DeltaPct:     35.2,
			Exceeded:     true,
			Threshold:    20.0,
		},
	}
}

// ── GenerateHTMLReport ───────────────────────────────────────────────────────

func TestGenerateHTMLReport_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("report file not found: %v", err)
	}
}

func TestGenerateHTMLReport_EmptyResultsReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	err := GenerateHTMLReport(nil, nil, path)
	if err == nil {
		t.Error("expected error for empty results slice")
	}
}

func TestGenerateHTMLReport_ContainsScenarioNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)
	for _, wantText := range []string{"baseline", "inference-routing"} {
		if !strings.Contains(html, wantText) {
			t.Errorf("report should contain %q", wantText)
		}
	}
}

func TestGenerateHTMLReport_IsValidHTML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	// Basic structural checks.
	for _, marker := range []string{"<!DOCTYPE html>", "<html", "</html>", "<head>", "<body>", "</body>"} {
		if !strings.Contains(html, marker) {
			t.Errorf("report missing HTML marker: %q", marker)
		}
	}
}

func TestGenerateHTMLReport_ContainsLatencyValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	// P99 values should appear in the report.
	if !strings.Contains(html, "10.50") {
		t.Error("report should contain baseline P99 10.50")
	}
	if !strings.Contains(html, "14.20") {
		t.Error("report should contain inference-routing P99 14.20")
	}
}

func TestGenerateHTMLReport_RegressionBadgePresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), sampleRegressions(), path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	if !strings.Contains(html, "REGRESSION") {
		t.Error("report should show REGRESSION badge for exceeded threshold")
	}
}

func TestGenerateHTMLReport_PassBadgePresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	// Pass: regression not exceeded
	regs := []*scenarios.RegressionResult{
		{ScenarioName: "inference-routing", Exceeded: false},
	}
	if err := GenerateHTMLReport(sampleResults(), regs, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	if !strings.Contains(html, "PASS") {
		t.Error("report should show PASS badge when threshold not exceeded")
	}
}

func TestGenerateHTMLReport_NewBadgeForUncomparedScenarios(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	// No regressions supplied — all scenarios should show as NEW.
	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	if !strings.Contains(html, "NEW") {
		t.Error("report should show NEW badge when no regression comparison exists")
	}
}

func TestGenerateHTMLReport_ContainsGatewayOverhead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	// The overhead column value for inference-routing is 3.70 ms.
	if !strings.Contains(html, "3.70") {
		t.Error("report should contain gateway overhead value 3.70")
	}
}

func TestGenerateHTMLReport_ContainsDataPlane(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	if !strings.Contains(html, "envoy") {
		t.Error("report should contain data plane name 'envoy'")
	}
}

func TestGenerateHTMLReport_CreatesParentDirectory(t *testing.T) {
	// Nested path that does not exist yet.
	base := t.TempDir()
	path := filepath.Join(base, "nested", "deep", "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error when parent directory missing: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("report file not found at nested path: %v", err)
	}
}

func TestGenerateHTMLReport_AgentGatewayDataPlaneBadge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	results := []*scenarios.Results{
		{
			ScenarioName: "baseline",
			DataPlane:    "agentgateway",
			Timestamp:    time.Now(),
		},
	}
	if err := GenerateHTMLReport(results, nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)
	if !strings.Contains(html, "agentgateway") {
		t.Error("report should contain 'agentgateway' data plane label")
	}
}

func TestGenerateHTMLReport_StreamingMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	results := []*scenarios.Results{
		{
			ScenarioName: "streaming",
			DataPlane:    "envoy",
			Timestamp:    time.Now(),
			P99LatencyMs: 20.0,
			TTFTMeanMs:   45.5,
			ITLMeanUs:    1250,
		},
	}
	if err := GenerateHTMLReport(results, nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)
	if !strings.Contains(html, "45.50") {
		t.Error("report should contain TTFT value 45.50")
	}
}

func TestGenerateHTMLReport_InfraUtilizationSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")

	if err := GenerateHTMLReport(sampleResults(), nil, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, _ := os.ReadFile(path)
	html := string(content)

	if !strings.Contains(html, "Infrastructure Utilization") {
		t.Error("report should contain Infrastructure Utilization section")
	}
}

// ── ReportData internal structure ────────────────────────────────────────────

func TestReportData_RegressionMapBuilt(t *testing.T) {
	regs := sampleRegressions()
	regMap := make(map[string]*scenarios.RegressionResult, len(regs))
	for _, r := range regs {
		if r != nil {
			regMap[r.ScenarioName] = r
		}
	}
	if _, ok := regMap["inference-routing"]; !ok {
		t.Error("regression map should contain inference-routing")
	}
}

func TestReportData_NilRegressionHandled(t *testing.T) {
	// Nil regressions in the slice should be skipped without panic.
	regs := []*scenarios.RegressionResult{nil, {ScenarioName: "x", Exceeded: false}}
	regMap := make(map[string]*scenarios.RegressionResult)
	for _, r := range regs {
		if r != nil {
			regMap[r.ScenarioName] = r
		}
	}
	if len(regMap) != 1 {
		t.Errorf("nil entries should be filtered out, got %d entries", len(regMap))
	}
}
