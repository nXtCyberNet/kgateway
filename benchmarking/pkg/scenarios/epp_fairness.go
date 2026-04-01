package scenarios

import "fmt"

// ExpectedFairnessDistribution is the target traffic share per tier for S5.
// The EPP should route ~70% to tier-large (lowest KV cache pressure / fastest),
// ~20% to tier-medium, and ~10% to tier-small (highest pressure / slowest).
var ExpectedFairnessDistribution = map[string]float64{
	"tier-large":  70.0,
	"tier-medium": 20.0,
	"tier-small":  10.0,
}

// FairnessTolerancePct is the maximum allowed deviation from ExpectedFairnessDistribution
// before CheckFairness reports a violation.
const FairnessTolerancePct = 5.0

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

	msg := "EPP fairness check failed:\n"
	for _, v := range violations {
		msg += "  - " + v + "\n"
	}
	return fmt.Errorf("%s", msg)
}
