//go:build !unix

package comprehension

import "context"

func lockGraphBuildFile(ctx context.Context, root string) (func(), error) {
	if err := checkBuildContext(ctx); err != nil {
		return nil, err
	}
	return func() {}, nil
}
