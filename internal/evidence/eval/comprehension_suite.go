package eval

func ComprehensionMemorySuite() SWESuite {
	maxBroad := 0
	maxHeavy := 0
	return SWESuite{
		Name:    "codebase-comprehension-memory",
		Version: 1,
		Coverage: SWECoverageContract{
			MinTasks:                   evalIntPtr(4),
			RequiredFamilies:           []string{"orientation", "localization", "planning", "failing_test"},
			RequiredTags:               []string{"context_graph", "context_pack", "runtime_trace", "grounding_quality", "token_reduction"},
			RequiredTools:              []string{"context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query"},
			RequiredScenarioAssertions: []string{"tools", "discovery_classes", "broad_tool_budget", "heavy_discovery_budget"},
		},
		Tasks: []SWETask{
			{
				ID:      "context-orient-subsystem",
				Family:  "orientation",
				Prompt:  "Explain the workspace context tool subsystem without reading broad source files.",
				Tags:    []string{"context_graph", "grounding_quality", "token_reduction", "read-only"},
				Fixture: "fixtures/comprehension/go-workspace",
				Scenario: Scenario{
					Name:                      "context graph subsystem orientation",
					PromptContains:            []string{"context tool subsystem"},
					RequireTools:              []string{"context_status", "context_query", "context_zoom"},
					RequireDiscoveryClasses:   []string{"context_status", "context_query", "context_zoom"},
					MaxBroadToolRequests:      &maxBroad,
					MaxHeavyDiscoveryRequests: &maxHeavy,
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
					MaxToolRequests:       evalIntPtr(5),
				},
			},
			{
				ID:      "context-localize-multilang-symbol",
				Family:  "localization",
				Prompt:  "Locate the implementation and tests for a cross-language helper symbol using graph relationships first.",
				Tags:    []string{"context_graph", "grounding_quality", "token_reduction", "read-only", "multi-language"},
				Fixture: "fixtures/comprehension/multilang-workspace",
				Scenario: Scenario{
					Name:                      "multi-language graph localization",
					PromptContains:            []string{"cross-language helper symbol"},
					RequireTools:              []string{"context_status", "context_query", "context_zoom"},
					RequireDiscoveryClasses:   []string{"context_status", "context_query", "context_zoom"},
					MaxBroadToolRequests:      &maxBroad,
					MaxHeavyDiscoveryRequests: &maxHeavy,
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
					MaxToolRequests:       evalIntPtr(6),
				},
			},
			{
				ID:      "context-pack-planning",
				Family:  "planning",
				Prompt:  "Create a bounded implementation plan for changing Run while preserving related tests.",
				Tags:    []string{"context_pack", "grounding_quality", "token_reduction", "read-only"},
				Fixture: "fixtures/comprehension/go-workspace",
				Scenario: Scenario{
					Name:                      "packet-ready context planning",
					PromptContains:            []string{"bounded implementation plan"},
					RequireTools:              []string{"context_status", "context_pack"},
					RequireDiscoveryClasses:   []string{"context_status", "context_pack"},
					MaxBroadToolRequests:      &maxBroad,
					MaxHeavyDiscoveryRequests: &maxHeavy,
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
					MaxToolRequests:       evalIntPtr(4),
				},
			},
			{
				ID:      "context-trace-failing-test",
				Family:  "failing_test",
				Prompt:  "Use the failing test output to localize the runtime failure before planning a fix.",
				Tags:    []string{"context_pack", "runtime_trace", "grounding_quality", "read-only"},
				Fixture: "fixtures/comprehension/failing-test",
				Scenario: Scenario{
					Name:                      "runtime trace enriched context pack",
					PromptContains:            []string{"failing test output"},
					RequireTools:              []string{"context_status", "context_trace_pack", "context_pack"},
					RequireDiscoveryClasses:   []string{"context_status", "runtime_trace", "context_pack"},
					MaxBroadToolRequests:      &maxBroad,
					MaxHeavyDiscoveryRequests: &maxHeavy,
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
					MaxToolRequests:       evalIntPtr(5),
				},
			},
			{
				ID:      "discovery-query-context-backed",
				Family:  "localization",
				Prompt:  "Run one impact analysis for changing a public helper and avoid repeated broad listing.",
				Tags:    []string{"context_graph", "context_pack", "grounding_quality", "token_reduction", "read-only"},
				Fixture: "fixtures/comprehension/go-workspace",
				Scenario: Scenario{
					Name:                      "context-backed discovery query",
					PromptContains:            []string{"impact analysis"},
					RequireTools:              []string{"context_status", "discovery_query"},
					RequireDiscoveryClasses:   []string{"context_status", "composite_query"},
					MaxBroadToolRequests:      &maxBroad,
					MaxHeavyDiscoveryRequests: &maxHeavy,
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
					MaxToolRequests:       evalIntPtr(4),
				},
			},
		},
	}
}

func evalIntPtr(value int) *int {
	return &value
}
