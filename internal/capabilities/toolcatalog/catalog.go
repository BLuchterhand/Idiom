package toolcatalog

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"idiom/internal/contracts/autonomy"
)

// ToolSurfaceSpec describes adapter-neutral tool-surface metadata used by
// policy, runtime projections, and manifest filtering. Adapter-specific
// schemas, descriptions, and request examples belong with the adapter that
// renders them.
type ToolSurfaceSpec struct {
	Tool                     string
	Language                 string
	Category                 int
	DefaultManifest          bool
	Narrowing                bool
	EvidenceCapabilities     []string
	CapabilityClassification string
	PolicyCapabilities       []autonomy.Capability
	SideEffects              []autonomy.SideEffectClass
	MCPDiscoveryProfile      bool
	DiscoveryLocalArtifacts  bool
	StaleContextErrors       bool
	MCPDiscoveryReadModel    bool
	RuntimeSourceInspection  bool
	RuntimeContextDiscovery  bool
	RuntimeHistoryInspection bool
	Timeout                  time.Duration
	PlannerAllowed           bool
	ActionClass              string
}

// ToolSurfaceCatalog is a clone-safe catalog for neutral tool-surface facts.
type ToolSurfaceCatalog struct {
	specs  []ToolSurfaceSpec
	byTool map[string]ToolSurfaceSpec
}

func NewToolSurfaceCatalog(specs []ToolSurfaceSpec) ToolSurfaceCatalog {
	out := make([]ToolSurfaceSpec, 0, len(specs))
	byTool := make(map[string]ToolSurfaceSpec, len(specs))
	for _, spec := range specs {
		spec.Tool = strings.TrimSpace(spec.Tool)
		cloned := cloneSpec(spec)
		out = append(out, cloned)
		if cloned.Tool != "" {
			byTool[cloned.Tool] = cloneSpec(cloned)
		}
	}

	return ToolSurfaceCatalog{specs: out, byTool: byTool}
}

func MustNewToolSurfaceCatalog(specs []ToolSurfaceSpec) ToolSurfaceCatalog {
	catalog := NewToolSurfaceCatalog(specs)
	if err := catalog.Validate(); err != nil {
		panic(err)
	}

	return catalog
}

func (c ToolSurfaceCatalog) Specs() []ToolSurfaceSpec {
	out := make([]ToolSurfaceSpec, len(c.specs))
	for i, spec := range c.specs {
		out[i] = cloneSpec(spec)
	}

	return out
}

func (c ToolSurfaceCatalog) Lookup(tool string) (ToolSurfaceSpec, bool) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ToolSurfaceSpec{}, false
	}

	if spec, ok := c.byTool[tool]; ok {
		return cloneSpec(spec), true
	}

	return ToolSurfaceSpec{}, false
}

func (c ToolSurfaceCatalog) ToolsWhere(match func(ToolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range c.specs {
		if match == nil || match(cloneSpec(spec)) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}

func (c ToolSurfaceCatalog) Validate() error {
	var problems []string
	seen := make(map[string]int, len(c.specs))

	for i, spec := range c.specs {
		tool := strings.TrimSpace(spec.Tool)
		if tool == "" {
			problems = append(problems, fmt.Sprintf("tool_surface_specs[%d].tool is required", i))
			continue
		}

		if first, ok := seen[tool]; ok {
			problems = append(problems, fmt.Sprintf("tool_surface_specs[%d].tool duplicates tool_surface_specs[%d].tool %q", i, first, tool))
			continue
		}

		seen[tool] = i
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid tool surface catalog: %s", strings.Join(problems, "; "))
	}

	return nil
}

func cloneSpec(spec ToolSurfaceSpec) ToolSurfaceSpec {
	spec.EvidenceCapabilities = slices.Clone(spec.EvidenceCapabilities)
	spec.PolicyCapabilities = slices.Clone(spec.PolicyCapabilities)
	spec.SideEffects = slices.Clone(spec.SideEffects)

	return spec
}
