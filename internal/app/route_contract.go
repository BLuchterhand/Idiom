package app

import (
	"encoding/json"
	"strings"

	"idiom/internal/agent"
)

type routeSource string

const (
	routeSourceTypedCommand         routeSource = "typed_command"
	routeSourceClassifier           routeSource = "classifier"
	routeSourceConservativeFallback routeSource = "conservative_fallback"
)

type routeMutationAuthority string

const (
	routeMutationAuthorityNone                 routeMutationAuthority = "none"
	routeMutationAuthorityExplicit             routeMutationAuthority = "explicit"
	routeMutationAuthorityConfirmationRequired routeMutationAuthority = "confirmation_required"
)

type routeContractProjection struct {
	Source             routeSource            `json:"source"`
	Family             string                 `json:"family"`
	Lane               string                 `json:"lane"`
	Contract           string                 `json:"contract"`
	MutationAuthority  routeMutationAuthority `json:"mutation_authority"`
	MutationCapable    bool                   `json:"mutation_capable"`
	RequestedOutcome   string                 `json:"requested_outcome,omitempty"`
	WriteIntent        string                 `json:"write_intent,omitempty"`
	ReadOnlyConstraint bool                   `json:"read_only_constraint,omitempty"`
	ContinuationTarget string                 `json:"continuation_target,omitempty"`
}

func projectRouteContract(plan turnRoutePlan, mode agent.Mode) routeContractProjection {
	plan = normalizeTurnRoutePlan(plan, mode)
	source := normalizeRouteSource(plan.RouteSource)
	authority := routeMutationAuthorityNone
	if isImplementationLikeFamily(plan.Family) {
		switch source {
		case routeSourceTypedCommand:
			authority = routeMutationAuthorityExplicit
		case routeSourceClassifier:
			authority = routeMutationAuthorityConfirmationRequired
		}
	}
	return routeContractProjection{
		Source:             source,
		Family:             strings.TrimSpace(plan.Family),
		Lane:               strings.TrimSpace(plan.InitialLane),
		Contract:           strings.TrimSpace(plan.Contract),
		MutationAuthority:  authority,
		MutationCapable:    authority == routeMutationAuthorityExplicit,
		RequestedOutcome:   strings.TrimSpace(plan.RequestedOutcome),
		WriteIntent:        strings.TrimSpace(plan.WriteIntent),
		ReadOnlyConstraint: plan.ReadOnlyConstraint,
		ContinuationTarget: strings.TrimSpace(plan.ContinuationTarget),
	}
}

func routeRequiresMutationConfirmation(plan promptExecutionPlan) bool {
	contract := projectRouteContract(plan.route, plan.mode)
	return contract.MutationAuthority == routeMutationAuthorityConfirmationRequired
}

func normalizeRouteSource(source routeSource) routeSource {
	switch source {
	case routeSourceTypedCommand, routeSourceClassifier, routeSourceConservativeFallback:
		return source
	default:
		return routeSourceConservativeFallback
	}
}

func renderRouteContractProjection(contract routeContractProjection) string {
	body, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	return string(body)
}
