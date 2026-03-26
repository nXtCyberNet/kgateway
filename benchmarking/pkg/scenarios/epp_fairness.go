package scenarios

import (
	"fmt"
	"math"
)

// GetEPPFairnessScenario returns the S5 (Fairness/Distribution) configuration.
func GetEPPFairnessScenario() *Scenario {
	return &Scenario{
		Name:                   "S5-EPP-Fairness",
		Description:            "Verifies traffic distribution across heterogeneous tiers",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
		TargetRPS:              100,
		DurationSeconds:        120,
		ConcurrentUsers:        10,
		WarmupSeconds:          30,
		BackendTiers: []BackendTier{
			{Name: "tier-large", CPULimit: "4", ResponseDelayMs: 50, Replicas: 2},
			{Name: "tier-medium", CPULimit: "2", ResponseDelayMs: 100, Replicas: 1},
			{Name: "tier-small", CPULimit: "0.5", ResponseDelayMs: 200, Replicas: 1},
		},
	}
}

// CheckFairness verifies that traffic is distributed close to expected ratios.
// actual: map of tier name to percentage (0.0-100.0)
// expected: map of tier name to expected percentage
// tolerancePct: allowed deviation (e.g., 5.0 for 5%)
func CheckFairness(actual map[string]float64, expected map[string]float64, tolerancePct float64) error {
	for tier, exp := range expected {
		act, ok := actual[tier]
		if !ok {
			return fmt.Errorf("missing data for expected tier: %s", tier)
		}
		diff := math.Abs(act - exp)
		if diff > tolerancePct {
			return fmt.Errorf("tier %s distribution mismatch: got %.2f%%, want %.2f%% (diff %.2f%% exceeds tolerance %.2f%%)",
				tier, act, exp, diff, tolerancePct)
		}
	}
	return nil
}
