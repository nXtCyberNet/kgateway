package scenarios

// GetHeaderRoutingScenario returns the S2 (Standard HTTP) configuration.
// It uses standard HTTP header matching (x-model-name) without inference-specific logic.
func GetHeaderRoutingScenario() *Scenario {
	return &Scenario{
		Name:                   "S2-Header-Routing",
		Description:            "Gateway enabled with standard HTTP header routing",
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
				Labels:          map[string]string{"tier": "large", "app": "llm-d-sim"},
			},
		},
	}
}
