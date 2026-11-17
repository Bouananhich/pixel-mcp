package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Path safety for the pipeline tools (export_layers, compose_from_manifest, run_lua).
//
// Rule: an output path must be absolute and, after cleaning and resolving symlinks
// of its longest existing ancestor, must lie inside baseDir (the directory of the
// source sprite or manifest) or inside one of the configured allowed_dirs.
// Older tools are not affected.

// resolvePath cleans an absolute path and resolves symlinks in its longest existing
// ancestor, so that e.g. /tmp/x and /private/tmp/x compare equal on macOS even when
// x does not exist yet.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		if rest == "" {
			rest = filepath.Base(cur)
		} else {
			rest = filepath.Join(filepath.Base(cur), rest)
		}
		cur = parent
	}
}

// isWithinDir reports whether path is dir itself or inside it (both resolved).
func isWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(resolvePath(dir), resolvePath(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// isUnderAllowedDir reports whether path lies inside one of the allowed directories.
func isUnderAllowedDir(path string, allowed []string) bool {
	for _, dir := range allowed {
		if dir != "" && isWithinDir(path, dir) {
			return true
		}
	}
	return false
}

// checkOutputPath validates an output path written by a pipeline tool.
//
// The path must be absolute and resolve inside baseDir or one of allowed.
func checkOutputPath(path, baseDir string, allowed []string) error {
	if path == "" {
		return fmt.Errorf("output path cannot be empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("output path must be absolute: %s", path)
	}
	if baseDir != "" && isWithinDir(path, baseDir) {
		return nil
	}
	if isUnderAllowedDir(path, allowed) {
		return nil
	}
	return fmt.Errorf("output path %s is outside %s and not covered by allowed_dirs in config", path, baseDir)
}
