package workspace

// DiscoveryLayer constants are the stable layer names emitted by
// discovery_query packets. Keep them at the packet producer boundary so runtime
// presenters and evidence scoring do not grow parallel string vocabularies.
const (
	DiscoveryLayerContextPack           = ToolNameContextPack
	DiscoveryLayerTargetListing         = "target_listing"
	DiscoveryLayerTextAnchorSearch      = "text_anchor_search"
	DiscoveryLayerSecurityTextSweep     = "security_text_sweep"
	DiscoveryLayerProjectBehaviorDigest = "project_behavior_digest"
	DiscoveryLayerProjectContextDigest  = "project_context_digest"
	DiscoveryLayerFileOutline           = "file_outline"
	DiscoveryLayerFileRead              = "file_read"
	DiscoveryLayerDependencyGraph       = ToolNameDependencyGraph
	DiscoveryLayerRelatedFiles          = "related_files"
	DiscoveryLayerHistorySymbol         = ToolNameHistorySymbol
	DiscoveryLayerHistoryRange          = ToolNameHistoryRange
	DiscoveryLayerBlameRange            = ToolNameBlameRange
	DiscoveryLayerStructuralSearch      = ToolNameStructuralSearch
	DiscoveryLayerCtagsSymbols          = ToolNameCtagsSymbols
	DiscoveryLayerSemgrepScan           = ToolNameSemgrepScan
	DiscoveryLayerJoernScan             = ToolNameJoernScan
)
