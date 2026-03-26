package scenarios

// S3InferenceRouting returns the configuration for Scenario 3: Full inference routing.
// Evaluates EPP scaling against three backend tiers using body parsing for model extraction.
func S3InferenceRouting() *Scenario {
	return &Scenario{
		Name:                   "inference-routing",
		Description:            "Full EPP active with body parsing, routing to 3 tiers.",
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
