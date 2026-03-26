// pkg/scenarios/header_routing.go
// Copyright 2026 The kgateway Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package scenarios

// S2HeaderRouting returns the header-routing scenario: gateway enabled, inference routing OFF,
// routes by x-model-name HTTP header. RPS and duration are identical to S1 so that
// the delta in P99 isolates pure L7 header-inspection overhead.
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
