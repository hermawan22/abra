package ingest

import (
	"errors"
	"fmt"
	"strings"
)

// SourceType identifies a class of source connector. Keep this generic so
// deployments can register private connectors without changing the registry.
type SourceType string

const (
	SourceTypeLocalRepo SourceType = "local_repo"
	SourceTypeMarkdown  SourceType = "markdown"
	SourceTypeGitRepo   SourceType = "git_repo"
	SourceTypeMCP       SourceType = "mcp"
)

// SourceSpec is the durable configuration for a knowledge source.
type SourceSpec struct {
	ID               string
	Type             SourceType
	Root             string
	Scope            string
	Include          []string
	Exclude          []string
	IncludeCode      bool
	CodeInclude      []string
	CodeExclude      []string
	MaxFileBytes     int64
	IncludeGenerated bool
	GitRemoteURL     string
	GitRef           string
	GitRevision      string
	GitProvider      string
	GitProjectPath   string
	GitDepth         int
	Metadata         map[string]string
}

// Validate checks connector-neutral registry constraints.
func (s SourceSpec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("source id is required")
	}
	if strings.TrimSpace(string(s.Type)) == "" {
		return fmt.Errorf("source %q type is required", s.ID)
	}
	if strings.TrimSpace(s.Scope) == "" {
		return fmt.Errorf("source %q scope is required", s.ID)
	}
	if s.Type == SourceTypeLocalRepo && strings.TrimSpace(s.Root) == "" {
		return fmt.Errorf("source %q root is required for %s", s.ID, SourceTypeLocalRepo)
	}
	if s.Type == SourceTypeGitRepo && strings.TrimSpace(s.Root) == "" && strings.TrimSpace(s.GitRemoteURL) == "" {
		return fmt.Errorf("source %q git_remote_url is required for %s", s.ID, SourceTypeGitRepo)
	}
	return nil
}

func cloneSource(source SourceSpec) SourceSpec {
	source.Include = append([]string(nil), source.Include...)
	source.Exclude = append([]string(nil), source.Exclude...)
	source.CodeInclude = append([]string(nil), source.CodeInclude...)
	source.CodeExclude = append([]string(nil), source.CodeExclude...)
	if source.Metadata != nil {
		source.Metadata = cloneMap(source.Metadata)
	}
	return source
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
