package cmdconfig

import (
	"os"

	"idiom/internal/platform/config"
)

// Load returns the configured workspace, falling back to the current directory
// when the CLI's default config file has not been created yet.
func Load(path string) (config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := config.Default()
		if wd, e := os.Getwd(); e == nil {
			wd = config.CanonicalizeExistingPath(wd)
			cfg.Workspace.Root = wd
			cfg.Workspace.CWD = wd
		}
		return cfg, nil
	}
	return config.Load(path)
}
