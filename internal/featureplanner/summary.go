package featureplanner

// SDDRefsForSummary returns stable traceability references for operator-facing
// completion summaries.
func SDDRefsForSummary(plan FeaturePlan) []string {
	seen := map[string]bool{}
	var refs []string
	add := func(values []string) {
		for _, value := range values {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			refs = append(refs, value)
		}
	}
	add(plan.SDDContract.Refs)
	add(plan.SDDContract.PRDRequirementRefs)
	add(plan.SDDContract.ADRRefs)
	for _, atom := range plan.Atoms {
		add(atom.SDDRefs)
	}
	return refs
}
