package scenarios

// S2HeaderRouting returns the configuration for Scenario 2: Standard header-based HTTP routing.
// No inference routing or body parsing overhead. Routes via x-model-name.
func S2HeaderRouting() *Scenario {
	s := S1Baseline()
	s.Name = "header-routing"
	s.Description = "Gateway enabled, standard HTTP header routing via x-model-name. No body parsing."
	s.EnableInferenceRouting = false
	s.EnableBodyParsing = false
	return s
}
