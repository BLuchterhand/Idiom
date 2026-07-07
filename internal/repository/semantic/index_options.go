package semantic

import "context"

type IndexOptions struct {
	Scope    string
	MaxFiles int
	Context  context.Context
}
