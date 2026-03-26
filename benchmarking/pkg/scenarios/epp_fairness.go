package scenarios

import (
	"fmt"
	"math"
)

// S5EPPFairness returns config for Scenario 5: Validating EPP probability fairness.
func S5EPPFairness() *Scenario {
	return &Scenario{
		Name:                   "epp-fairness",
		Description:            "Measures EPP selection distribution checking fairness algorithms.",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
		TargetRPS:              50,
		DurationSeconds:        120,
		ConcurrentUsers:        10,
		WarmupSeconds:          30,
		BackendTiers: []BackendTier{
			{
				Name:            "tier-large",
				CPULimit:        "4",
				MemoryLimit:     "4Gi",
				ResponseDelayMs: 50,
				Replicas:        1,
				Labels:          map[string]string{"app": "llm-d-sim", "tier": "large"},
			},
			{
				Name:            "tier-medium",
				CPULimit:        "2",
				MemoryLimit:     "2Gi",
				ResponseDelayMs: 100,
				Replicas:        1,
				Labels:          map[string]string{"app": "llm-d-sim", "tier": "medium"},
			},
			{
				Name:            "tier-small",
				CPULimit:        "0.5",
				MemoryLimit:     "512Mi",
				ResponseDelayMs: 200,
				Replicas:        1,
				Labels:          map[string]string{"app": "llm-d-sim", "tier": "small"},
			},
		},
	}
}

// CheckFairness verifies actual traffic distribution against expected thresholds
func CheckFairness(actual map[string]float64, expected map[string]float64, tolerancePct float64) error {
	for name, expRate := range expected {
		actRate, found := actual[name]
		if !found {
			return fmt.Errorf("missing metric for backend %s", name)
		}
		
		diff := math.Abs(actRate - expRate)
		if diff > tolerancePct {
			return fmt.Errorf("fairness violated for %s: expect=%.1f%%, actual=%.1f%% (diff=%.1f%%, tol=%.1f%%)",
				name, expRate, actRate, diff, tolerancePct)
		}
	}
	return nil
}
