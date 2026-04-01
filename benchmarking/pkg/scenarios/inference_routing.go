package scenarios

// S3InferenceRouting returns the full EPP scenario: body parsing on, InferenceModel/InferencePool
// enabled, three heterogeneous backend tiers. ResponseDelayMs models MIG slice capability
// (low delay = large slice, high delay = constrained slice). SimulatedKVCachePercent creates
// realistic pressure skew so the EPP makes non-trivial routing decisions.
func S3InferenceRouting() *Scenario {
	return &Scenario{
		Name:                   "inference-routing",
		Description:            "Full EPP routing with body parsing across three heterogeneous backend tiers",
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
				Replicas:                2,
				Labels:                  map[string]string{"app": "llm-d-sim", "tier": "large"},
				SimulatedKVCachePercent: 20,
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
				SimulatedKVCachePercent: 80,
			},
		},
	}
}
