package scenarios

// GetBaselineScenario returns the S1 (TCP Baseline) configuration.
// This scenario bypasses the gateway to establish the raw performance of the simulator.
func GetBaselineScenario() *Scenario {
	return &Scenario{
		Name:                   "S1-TCP-Baseline",
		Description:            "Direct connection to simulator pod bypassing kgateway",
		TargetRPS:              100,
		DurationSeconds:        120,
		ConcurrentUsers:        10,
		WarmupSeconds:          60,
		EnableInferenceRouting: false,
		EnableBodyParsing:      false,
		BackendTiers: []BackendTier{
			{
				Name:            "tier-large",
				CPULimit:        "4",
				MemoryLimit:     "4Gi",
				ResponseDelayMs: 50,
				Replicas:        1,
				Labels:          map[string]string{"tier": "large", "app": "llm-d-sim"},
			},
		},
	}
}
