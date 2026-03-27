// pkg/scenarios/epp_fairness_test.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"strings"
	"testing"
)

// ── CheckFairness ────────────────────────────────────────────────────────────

func TestCheckFairness_ExactMatch(t *testing.T) {
	actual := map[string]float64{
		"tier-large":  70.0,
		"tier-medium": 20.0,
		"tier-small":  10.0,
	}
	if err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct); err != nil {
		t.Errorf("expected no error for exact match, got: %v", err)
	}
}

func TestCheckFairness_WithinTolerance(t *testing.T) {
	// Each tier deviates by exactly FairnessTolerancePct (should still pass).
	actual := map[string]float64{
		"tier-large":  70.0 + FairnessTolerancePct,
		"tier-medium": 20.0,
		"tier-small":  10.0 - FairnessTolerancePct,
	}
	// At exactly the boundary the delta == tolerance, which is NOT > tolerance, so should pass.
	if err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct); err != nil {
		t.Errorf("exact-boundary deviation should pass, got: %v", err)
	}
}

func TestCheckFairness_ExceedsTolerance(t *testing.T) {
	actual := map[string]float64{
		"tier-large":  50.0, // way off
		"tier-medium": 20.0,
		"tier-small":  10.0,
	}
	err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct)
	if err == nil {
		t.Error("expected error when tier-large deviates by 20%")
	}
	if !strings.Contains(err.Error(), "tier-large") {
		t.Errorf("error should mention tier-large, got: %v", err)
	}
}

func TestCheckFairness_MissingTier(t *testing.T) {
	// When a tier has no data at all it should report a violation.
	actual := map[string]float64{
		"tier-large": 70.0,
		// tier-medium and tier-small missing
	}
	err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct)
	if err == nil {
		t.Error("expected error for missing tiers")
	}
	if !strings.Contains(err.Error(), "tier-medium") && !strings.Contains(err.Error(), "tier-small") {
		t.Errorf("error should mention missing tiers, got: %v", err)
	}
}

func TestCheckFairness_AllTiersViolate(t *testing.T) {
	actual := map[string]float64{
		"tier-large":  10.0,
		"tier-medium": 10.0,
		"tier-small":  80.0,
	}
	err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct)
	if err == nil {
		t.Error("expected error when all tiers violate expected distribution")
	}
}

func TestCheckFairness_EmptyActual(t *testing.T) {
	err := CheckFairness(map[string]float64{}, ExpectedFairnessDistribution, FairnessTolerancePct)
	if err == nil {
		t.Error("empty actual distribution should result in violation")
	}
}

func TestCheckFairness_EmptyExpected(t *testing.T) {
	// No expectations means nothing can violate.
	actual := map[string]float64{"tier-large": 100.0}
	if err := CheckFairness(actual, map[string]float64{}, FairnessTolerancePct); err != nil {
		t.Errorf("empty expected distribution should pass, got: %v", err)
	}
}

func TestCheckFairness_ZeroTolerance(t *testing.T) {
	// Zero tolerance: even a 0.001% deviation should fail.
	actual := map[string]float64{
		"tier-large":  70.1,
		"tier-medium": 20.0,
		"tier-small":  9.9,
	}
	err := CheckFairness(actual, ExpectedFairnessDistribution, 0)
	if err == nil {
		t.Error("expected error with zero tolerance and non-zero deviation")
	}
}

func TestCheckFairness_ErrorContainsPolicyText(t *testing.T) {
	actual := map[string]float64{
		"tier-large":  10.0,
		"tier-medium": 20.0,
		"tier-small":  70.0,
	}
	err := CheckFairness(actual, ExpectedFairnessDistribution, FairnessTolerancePct)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "EPP fairness check failed") {
		t.Errorf("error should contain 'EPP fairness check failed', got: %v", msg)
	}
}

// ── S5EPPFairness factory ────────────────────────────────────────────────────

func TestS5EPPFairness_ThreeTiers(t *testing.T) {
	s := S5EPPFairness()
	if len(s.BackendTiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(s.BackendTiers))
	}
	names := make([]string, len(s.BackendTiers))
	for i, tier := range s.BackendTiers {
		names[i] = tier.Name
	}
	// Verify all three tiers are present
	for _, want := range []string{"tier-large", "tier-medium", "tier-small"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing tier: %q", want)
		}
	}
}

func TestS5EPPFairness_KVCachePressureOrdering(t *testing.T) {
	s := S5EPPFairness()
	tierMap := make(map[string]int, 3)
	for _, tier := range s.BackendTiers {
		tierMap[tier.Name] = tier.SimulatedKVCachePercent
	}
	if tierMap["tier-large"] >= tierMap["tier-medium"] {
		t.Errorf("tier-large KV cache (%d) should be less than tier-medium (%d)",
			tierMap["tier-large"], tierMap["tier-medium"])
	}
	if tierMap["tier-medium"] >= tierMap["tier-small"] {
		t.Errorf("tier-medium KV cache (%d) should be less than tier-small (%d)",
			tierMap["tier-medium"], tierMap["tier-small"])
	}
}

func TestS5EPPFairness_ResponseDelayOrdering(t *testing.T) {
	s := S5EPPFairness()
	delayMap := make(map[string]int, 3)
	for _, tier := range s.BackendTiers {
		delayMap[tier.Name] = tier.ResponseDelayMs
	}
	// More pressure = higher delay (tier-small should be slowest)
	if delayMap["tier-large"] >= delayMap["tier-medium"] {
		t.Errorf("tier-large delay (%d) should be less than tier-medium (%d)",
			delayMap["tier-large"], delayMap["tier-medium"])
	}
	if delayMap["tier-medium"] >= delayMap["tier-small"] {
		t.Errorf("tier-medium delay (%d) should be less than tier-small (%d)",
			delayMap["tier-medium"], delayMap["tier-small"])
	}
}

func TestExpectedFairnessDistribution_SumsToHundred(t *testing.T) {
	var total float64
	for _, pct := range ExpectedFairnessDistribution {
		total += pct
	}
	if total != 100.0 {
		t.Errorf("ExpectedFairnessDistribution should sum to 100, got %.1f", total)
	}
}
