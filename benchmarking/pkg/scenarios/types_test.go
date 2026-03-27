// pkg/scenarios/types_test.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to a temp YAML file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "scenario-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// ── LoadFromYAML ────────────────────────────────────────────────────────────

func TestLoadFromYAML_ValidBaseline(t *testing.T) {
	yaml := `
name: baseline
description: direct routing
gatewayClass: kgateway
enableInferenceRouting: false
enableBodyParsing: false
targetRPS: 100
durationSeconds: 120
concurrentUsers: 10
warmupSeconds: 15
backendTiers:
  - name: tier-large
    cpuLimit: "2000m"
    memoryLimit: "2Gi"
    responseDelayMs: 20
    replicas: 1
    labels:
      tier: large
    simulatedKVCachePercent: 30
`
	path := writeTemp(t, yaml)
	s, err := LoadFromYAML(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "baseline" {
		t.Errorf("name: got %q, want %q", s.Name, "baseline")
	}
	if s.TargetRPS != 100 {
		t.Errorf("targetRPS: got %d, want 100", s.TargetRPS)
	}
	if s.DurationSeconds != 120 {
		t.Errorf("durationSeconds: got %d, want 120", s.DurationSeconds)
	}
	if len(s.BackendTiers) != 1 {
		t.Fatalf("backendTiers: got %d, want 1", len(s.BackendTiers))
	}
	if s.BackendTiers[0].SimulatedKVCachePercent != 30 {
		t.Errorf("simulatedKVCachePercent: got %d, want 30", s.BackendTiers[0].SimulatedKVCachePercent)
	}
	if s.EnableInferenceRouting {
		t.Error("enableInferenceRouting should be false")
	}
}

func TestLoadFromYAML_InferenceRoutingEnabled(t *testing.T) {
	yaml := `
name: inference-routing
description: full EPP
gatewayClass: kgateway
enableInferenceRouting: true
enableBodyParsing: true
targetRPS: 50
durationSeconds: 60
concurrentUsers: 5
warmupSeconds: 10
backendTiers:
  - name: tier-large
    cpuLimit: "4"
    memoryLimit: "4Gi"
    responseDelayMs: 50
    replicas: 2
    labels:
      app: llm-d-sim
      tier: large
    simulatedKVCachePercent: 20
  - name: tier-small
    cpuLimit: "500m"
    memoryLimit: "512Mi"
    responseDelayMs: 200
    replicas: 1
    labels:
      app: llm-d-sim
      tier: small
    simulatedKVCachePercent: 80
`
	path := writeTemp(t, yaml)
	s, err := LoadFromYAML(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.EnableInferenceRouting {
		t.Error("enableInferenceRouting should be true")
	}
	if !s.EnableBodyParsing {
		t.Error("enableBodyParsing should be true")
	}
	if len(s.BackendTiers) != 2 {
		t.Fatalf("backendTiers: got %d, want 2", len(s.BackendTiers))
	}
	if s.BackendTiers[1].ResponseDelayMs != 200 {
		t.Errorf("tier-small responseDelayMs: got %d, want 200", s.BackendTiers[1].ResponseDelayMs)
	}
}

func TestLoadFromYAML_FileNotFound(t *testing.T) {
	_, err := LoadFromYAML(context.Background(), filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFromYAML_InvalidYAML(t *testing.T) {
	path := writeTemp(t, ":::invalid yaml:::")
	_, err := LoadFromYAML(context.Background(), path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadFromYAML_UnknownField(t *testing.T) {
	// KnownFields(true) should reject unknown keys.
	yaml := `
name: test
targetRPS: 10
durationSeconds: 30
backendTiers:
  - name: t
    responseDelayMs: 0
    replicas: 1
unknownField: oops
`
	path := writeTemp(t, yaml)
	_, err := LoadFromYAML(context.Background(), path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadFromYAML_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	_, err := LoadFromYAML(ctx, "anything.yaml")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── validate() ──────────────────────────────────────────────────────────────

func TestValidate_MissingName(t *testing.T) {
	s := &Scenario{TargetRPS: 10, DurationSeconds: 60, BackendTiers: []BackendTier{{Name: "t"}}}
	if err := s.validate(); err == nil {
		t.Error("expected error for missing name")
	}
}

func TestValidate_ZeroTargetRPS(t *testing.T) {
	s := &Scenario{Name: "x", TargetRPS: 0, DurationSeconds: 60, BackendTiers: []BackendTier{{Name: "t"}}}
	if err := s.validate(); err == nil {
		t.Error("expected error for zero targetRPS")
	}
}

func TestValidate_NegativeTargetRPS(t *testing.T) {
	s := &Scenario{Name: "x", TargetRPS: -1, DurationSeconds: 60, BackendTiers: []BackendTier{{Name: "t"}}}
	if err := s.validate(); err == nil {
		t.Error("expected error for negative targetRPS")
	}
}

func TestValidate_ZeroDuration(t *testing.T) {
	s := &Scenario{Name: "x", TargetRPS: 10, DurationSeconds: 0, BackendTiers: []BackendTier{{Name: "t"}}}
	if err := s.validate(); err == nil {
		t.Error("expected error for zero durationSeconds")
	}
}

func TestValidate_NoTiers(t *testing.T) {
	s := &Scenario{Name: "x", TargetRPS: 10, DurationSeconds: 60, BackendTiers: nil}
	if err := s.validate(); err == nil {
		t.Error("expected error for empty backendTiers")
	}
}

func TestValidate_TierMissingName(t *testing.T) {
	s := &Scenario{
		Name:            "x",
		TargetRPS:       10,
		DurationSeconds: 60,
		BackendTiers:    []BackendTier{{Name: "", ResponseDelayMs: 0}},
	}
	if err := s.validate(); err == nil {
		t.Error("expected error for tier with empty name")
	}
}

func TestValidate_NegativeResponseDelay(t *testing.T) {
	s := &Scenario{
		Name:            "x",
		TargetRPS:       10,
		DurationSeconds: 60,
		BackendTiers:    []BackendTier{{Name: "t", ResponseDelayMs: -1}},
	}
	if err := s.validate(); err == nil {
		t.Error("expected error for negative responseDelayMs")
	}
}

func TestValidate_KVCacheBelowZero(t *testing.T) {
	s := &Scenario{
		Name:            "x",
		TargetRPS:       10,
		DurationSeconds: 60,
		BackendTiers:    []BackendTier{{Name: "t", SimulatedKVCachePercent: -1}},
	}
	if err := s.validate(); err == nil {
		t.Error("expected error for KVCache < 0")
	}
}

func TestValidate_KVCacheAbove100(t *testing.T) {
	s := &Scenario{
		Name:            "x",
		TargetRPS:       10,
		DurationSeconds: 60,
		BackendTiers:    []BackendTier{{Name: "t", SimulatedKVCachePercent: 101}},
	}
	if err := s.validate(); err == nil {
		t.Error("expected error for KVCache > 100")
	}
}

func TestValidate_KVCacheBoundary(t *testing.T) {
	for _, pct := range []int{0, 50, 100} {
		s := &Scenario{
			Name:            "x",
			TargetRPS:       10,
			DurationSeconds: 60,
			BackendTiers:    []BackendTier{{Name: "t", SimulatedKVCachePercent: pct}},
		}
		if err := s.validate(); err != nil {
			t.Errorf("KVCache=%d should be valid but got: %v", pct, err)
		}
	}
}

func TestValidate_Valid(t *testing.T) {
	s := &Scenario{
		Name:            "baseline",
		TargetRPS:       100,
		DurationSeconds: 120,
		BackendTiers: []BackendTier{
			{Name: "tier-large", ResponseDelayMs: 0, SimulatedKVCachePercent: 30},
		},
	}
	if err := s.validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── Scenario factory functions produce valid configs ─────────────────────────

func TestS1Baseline_IsValid(t *testing.T) {
	s := S1Baseline()
	if s.Name != "baseline" {
		t.Errorf("expected name=baseline, got %q", s.Name)
	}
	if s.EnableInferenceRouting {
		t.Error("baseline should not have inference routing enabled")
	}
	if err := s.validate(); err != nil {
		t.Errorf("S1Baseline invalid: %v", err)
	}
}

func TestS3InferenceRouting_IsValid(t *testing.T) {
	s := S3InferenceRouting()
	if s.Name != "inference-routing" {
		t.Errorf("expected name=inference-routing, got %q", s.Name)
	}
	if !s.EnableInferenceRouting {
		t.Error("inference-routing should have inference routing enabled")
	}
	if len(s.BackendTiers) < 2 {
		t.Errorf("expected at least 2 tiers, got %d", len(s.BackendTiers))
	}
	if err := s.validate(); err != nil {
		t.Errorf("S3InferenceRouting invalid: %v", err)
	}
}

func TestS4Streaming_IsValid(t *testing.T) {
	s := S4Streaming()
	if s.Name != "streaming" {
		t.Errorf("expected name=streaming, got %q", s.Name)
	}
	if !s.EnableBodyParsing {
		t.Error("streaming scenario should enable body parsing")
	}
	if err := s.validate(); err != nil {
		t.Errorf("S4Streaming invalid: %v", err)
	}
}

func TestS5EPPFairness_IsValid(t *testing.T) {
	s := S5EPPFairness()
	if s.Name != "epp-fairness" {
		t.Errorf("expected name=epp-fairness, got %q", s.Name)
	}
	if len(s.BackendTiers) != 3 {
		t.Errorf("expected 3 tiers (large/medium/small), got %d", len(s.BackendTiers))
	}
	// KV cache pressure should be strictly ordered: large < medium < small
	large := s.BackendTiers[0].SimulatedKVCachePercent
	medium := s.BackendTiers[1].SimulatedKVCachePercent
	small := s.BackendTiers[2].SimulatedKVCachePercent
	if !(large < medium && medium < small) {
		t.Errorf("KV cache pressure should increase tier-large < tier-medium < tier-small, got %d/%d/%d", large, medium, small)
	}
	if err := s.validate(); err != nil {
		t.Errorf("S5EPPFairness invalid: %v", err)
	}
}
