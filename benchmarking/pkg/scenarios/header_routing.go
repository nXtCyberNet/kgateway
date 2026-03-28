package scenarios

func S2HeaderRouting() *Scenario {
	return &Scenario{
		Name:                   "header-routing",
		Description:            "Gateway routing by x-model-name header, inference extensions disabled",
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
