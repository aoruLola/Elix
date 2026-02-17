package policy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Policy struct {
	WorkspaceRoots []string
}

type RunOptions struct {
	Model         string
	Profile       string
	Sandbox       string
	SchemaVersion string
}

var safeOptionValue = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func New(roots []string) *Policy {
	return &Policy{WorkspaceRoots: roots}
}

func (p *Policy) ValidateWorkspace(path string) error {
	if path == "" {
		return fmt.Errorf("workspace_path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	for _, root := range p.WorkspaceRoots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if isWithinRoot(absRoot, absPath) {
			return nil
		}
	}
	return fmt.Errorf("workspace path %q is outside allowed roots", absPath)
}

func (p *Policy) ValidateRunOptions(opts RunOptions) error {
	if opts.Model != "" && !safeOptionValue.MatchString(opts.Model) {
		return fmt.Errorf("invalid model option")
	}
	if opts.Profile != "" && !safeOptionValue.MatchString(opts.Profile) {
		return fmt.Errorf("invalid profile option")
	}
	if opts.Sandbox != "" {
		switch opts.Sandbox {
		case "read-only", "workspace-write", "danger-full-access":
		default:
			return fmt.Errorf("invalid sandbox option")
		}
	}
	if opts.SchemaVersion != "" {
		switch opts.SchemaVersion {
		case "v1", "v2":
		default:
			return fmt.Errorf("invalid schema_version option")
		}
	}
	return nil
}

func isWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
