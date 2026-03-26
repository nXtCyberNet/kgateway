package scenarios

// GetInferenceRoutingScenario returns the S3 (Full EPP) configuration.
// This tests the full gateway tax including request body parsing and EPP header injection.
func GetInferenceRoutingScenario() *Scenario {
	return &Scenario{
		Name:                   "S3-Inference-Routing",
		Description:            "Gateway enabled with full EPP and request body parsing",
		GatewayClass:           "kgateway",
		EnableInferenceRouting: true,
		EnableBodyParsing:      true,
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
				Labels:          map[string]string{"tier": "large", "app": "llm-d-sim"},
			},
			{
				Name:            "tier-medium",
				CPULimit:        "2",
				MemoryLimit:     "2Gi",
				ResponseDelayMs: 100,
				Replicas:        1,
				Labels:          map[string]string{"tier": "medium", "app": "llm-d-sim"},
			},
			{
				Name:            "tier-small",
				CPULimit:        "500m",
				MemoryLimit:     "512Mi",
				ResponseDelayMs: 200,
				Replicas:        1,
				Labels:          map[string]string{"tier": "small", "app": "llm-d-sim"},
			},
		},
	}
}
