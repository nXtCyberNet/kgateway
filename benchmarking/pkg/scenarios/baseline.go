package scenarios

// S1Baseline returns the TCP baseline scenario: no inference extensions, direct L7 routing
// to a single tier-large backend. This is the performance floor every other scenario is
// compared against to calculate GatewayOverheadMs.
func S1Baseline() *Scenario {
	return &Scenario{
		Name:                   "baseline",
		Description:            "Direct L7 routing without inference extensions — establishes the performance floor",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: false,
		EnableBodyParsing:      false,
		TargetRPS:              100,
		DurationSeconds:        120,
		ConcurrentUsers:        10,
		WarmupSeconds:          60,
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
