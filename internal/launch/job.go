package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RenderedJob struct {
	Script           string
	WorkingDirectory string
	StandardOutput   string
	StandardError    string
	Environment      map[string]string
	ScratchDirectory string
	LogDirectory     string
}

func (r RenderedJob) PrepareDirectories() error {
	for _, dir := range []string{r.ScratchDirectory, r.LogDirectory} {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("refusing to create non-absolute directory %q", dir)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create job directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure job directory %s: %w", dir, err)
		}
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
