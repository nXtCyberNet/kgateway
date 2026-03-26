// pkg/scenarios/epp_fairness.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import "fmt"

// S5EPPFairness returns the EPP fairness scenario. Three tiers with deliberately skewed
// KV cache pressure (10% / 50% / 90%) and response delays (50ms / 100ms / 200ms) force
// the EPP to make non-trivial routing decisions. CheckFairness then validates that actual
// traffic distribution matches the expected 70/20/10 weights within FairnessTolerancePct.
func S5EPPFairness() *Scenario {
	return &Scenario{
		Name:                   "epp-fairness",
		Description:            "EPP weighted routing fairness across three tiers with skewed KV cache pressure",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
		TargetRPS:              100,
		DurationSeconds:        120,
		ConcurrentUsers:        10,
		WarmupSeconds:          60,
		BackendTiers: []BackendTier{
			{
				Name:                    "tier-large",
				CPULimit:                "4",
				MemoryLimit:             "4Gi",
				ResponseDelayMs:         50,
				Replicas:                1,
				Labels:                  map[string]string{"app": "llm-d-sim", "tier": "large"},
				SimulatedKVCachePercent: 10,
			},
			{
				Name:                    "tier-medium",
				CPULimit:                "2",
				MemoryLimit:             "2Gi",
				ResponseDelayMs:         100,
				Replicas:                1,
				Labels:                  map[string]string{"app": "llm-d-sim", "tier": "medium"},
				SimulatedKVCachePercent: 50,
			},
			{
				Name:                    "tier-small",
				CPULimit:                "500m",
				MemoryLimit:             "512Mi",
				ResponseDelayMs:         200,
				Replicas:                1,
				Labels:                  map[string]string{"app": "llm-d-sim", "tier": "small"},
				SimulatedKVCachePercent: 90,
			},
		},
	}
}

// CheckFairness validates that the actual per-tier traffic distribution matches expected
// weights within tolerancePct. actual and expected are maps of tier name → percentage (0-100).
// Returns an error listing every tier that exceeded the tolerance.
func CheckFairness(actual, expected map[string]float64, tolerancePct float64) error {
	var violations []string

	for tier, want := range expected {
		got, ok := actual[tier]
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: expected %.1f%% but got no data", tier, want))
			continue
		}
		delta := got - want
		if delta < 0 {
			delta = -delta
		}
		if delta > tolerancePct {
			violations = append(violations, fmt.Sprintf(
				"%s: expected %.1f%% got %.1f%% (delta %.1f%% exceeds tolerance %.1f%%)",
				tier, want, got, delta, tolerancePct,
			))
		}
	}

	if len(violations) == 0 {
		return nil
	}

	result := "EPP fairness check failed:\n"
	for _, v := range violations {
		result += "  - " + v + "\n"
	}
	return fmt.Errorf("%s", result)
}
