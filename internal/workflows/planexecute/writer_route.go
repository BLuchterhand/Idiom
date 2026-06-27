package planexecute

import (
	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
)

type WriterRouteDecisionInput struct {
	Config            config.Config
	Options           Options
	RequiredEditPaths []string
}

type WriterRouteDecider func(WriterRouteDecisionInput) writerroute.Decision
