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
	routeMutationAuthorityNone            routeMutationAuthority = "none"
	routeMutationAuthorityExplicit        routeMutationAuthority = "explicit"
	routeMutationAuthorityClassifierOwned routeMutationAuthority = "classifier_owned"
)

type routeContractProjection struct {
	Source            routeSource            `json:"source"`
	Family            string                 `json:"family"`
	Lane              string                 `json:"lane"`
	Contract          string                 `json:"contract"`
	MutationAuthority routeMutationAuthority `json:"mutation_authority"`
	MutationCapable   bool                   `json:"mutation_capable"`
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
			authority = routeMutationAuthorityClassifierOwned
		}
	}
	return routeContractProjection{
		Source:            source,
		Family:            strings.TrimSpace(plan.Family),
		Lane:              strings.TrimSpace(plan.InitialLane),
		Contract:          strings.TrimSpace(plan.Contract),
		MutationAuthority: authority,
		MutationCapable:   authority != routeMutationAuthorityNone,
	}
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
