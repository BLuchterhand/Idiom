package implementationdispatch

type PlanDocument struct {
	Path                string
	Content             string
	EditPaths           []string
	AuthorizedNewFiles  []string
	TestObligations     []string
	ReferenceOnly       bool
	AllowDirtyAtomScope bool
}
