package scenarios

// S1Baseline returns the configuration for Scenario 1: TCP baseline without gateway
// routing logic. It defines a direct connection to a single large sim pod.
func S1Baseline() *Scenario {
	return &Scenario{
		Name:                   "baseline",
		Description:            "TCP baseline without gateway routing, direct to large sim pod.",
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
				Replicas:        1,
				Labels:          map[string]string{"app": "llm-d-sim", "tier": "large"},
			},
		},
	}
}
