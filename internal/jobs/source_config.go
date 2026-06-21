package jobs

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/hermawan22/abra/internal/ingest"
)

type SourceConfig struct {
	ID             string
	Scope          string
	SourceType     ingest.SourceType
	Name           string
	BaseURL        string
	Authority      string
	AuthorityScore float64
	Config         map[string]any
	Metadata       map[string]any
}

func (s SourceConfig) ValidateIngestContract() error {
	switch s.SourceType {
	case ingest.SourceTypeLocalRepo, ingest.SourceTypeMarkdown, ingest.SourceTypeGitRepo:
		spec, err := s.IngestSpec()
		if err != nil {
			return err
		}
		if err := spec.Validate(); err != nil {
			return err
		}
		return nil
	case "":
		return fmt.Errorf("source %q source_type is required", s.ID)
	default:
		// Non-core source types are accepted as connector overlay configs. The
		// OSS worker only schedules markdown/local_repo/git_repo; overlays own validation.
		return nil
	}
}

func (s SourceConfig) IngestSpec() (ingest.SourceSpec, error) {
	remoteURL := firstString(s.Config, "git_remote_url", "remote_url", "repository_url", "repo_url")
	if remoteURL == "" && s.SourceType == ingest.SourceTypeGitRepo {
		remoteURL = strings.TrimSpace(s.BaseURL)
	}
	root, err := s.root()
	if err != nil {
		if s.SourceType != ingest.SourceTypeGitRepo {
			return ingest.SourceSpec{}, err
		}
		root = ""
	}
	if root == "" && s.SourceType != ingest.SourceTypeGitRepo {
		return ingest.SourceSpec{}, fmt.Errorf("source %q root is required for local markdown ingestion", s.ID)
	}

	spec := ingest.SourceSpec{
		ID:               s.ID,
		Type:             s.SourceType,
		Root:             root,
		Scope:            s.Scope,
		Include:          stringSlice(s.Config["include"]),
		Exclude:          stringSlice(s.Config["exclude"]),
		IncludeCode:      boolValue(s.Config["include_code"]),
		CodeInclude:      stringSlice(s.Config["code_include"]),
		CodeExclude:      stringSlice(s.Config["code_exclude"]),
		MaxFileBytes:     int64(intValue(s.Config["max_file_bytes"], int(ingest.DefaultMaxFileBytes))),
		IncludeGenerated: includeGeneratedFiles(s.Config),
		GitRemoteURL:     remoteURL,
		GitRef:           firstString(s.Config, "git_ref", "ref", "branch"),
		GitRevision:      firstString(s.Config, "git_revision", "revision", "commit", "sha"),
		GitProvider:      firstString(s.Config, "git_provider", "provider"),
		GitProjectPath:   firstString(s.Config, "git_project_path", "project_path", "repo_path"),
		GitDepth:         intValue(s.Config["git_depth"], 1),
		Metadata:         sourceSpecMetadata(s),
	}
	if len(spec.Include) == 0 {
		spec.Include = stringSlice(s.Config["includes"])
	}
	if len(spec.Exclude) == 0 {
		spec.Exclude = stringSlice(s.Config["excludes"])
	}
	if len(spec.CodeInclude) == 0 {
		spec.CodeInclude = stringSlice(s.Config["code_includes"])
	}
	if len(spec.CodeExclude) == 0 {
		spec.CodeExclude = stringSlice(s.Config["code_excludes"])
	}
	return spec, nil
}

func includeGeneratedFiles(config map[string]any) bool {
	if boolValue(config["include_generated"]) {
		return true
	}
	if value, ok := config["skip_generated_files"]; ok {
		return !boolValue(value)
	}
	return false
}

func (s SourceConfig) root() (string, error) {
	for _, key := range []string{"root", "path", "directory"} {
		if value := stringValue(s.Config[key]); value != "" {
			return value, nil
		}
	}
	if s.BaseURL == "" {
		return "", nil
	}
	u, err := url.Parse(s.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parse source %q base_url: %w", s.ID, err)
	}
	switch u.Scheme {
	case "":
		return s.BaseURL, nil
	case "file":
		return filepath.FromSlash(u.Path), nil
	default:
		if s.SourceType == ingest.SourceTypeGitRepo {
			return "", nil
		}
		return "", fmt.Errorf("source %q base_url must be a local path or file URL, got %q", s.ID, s.BaseURL)
	}
}

func sourceSpecMetadata(source SourceConfig) map[string]string {
	metadata := map[string]string{
		"source_config_id":   source.ID,
		"source_config_name": source.Name,
	}
	for key, value := range source.Metadata {
		if text := stringValue(value); text != "" {
			metadata[key] = text
		}
	}
	return metadata
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return cleanStrings(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				values = append(values, text)
			}
		}
		return values
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return cleanStrings(strings.Split(typed, ","))
	default:
		return nil
	}
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case int64:
		if typed > 0 {
			return int(typed)
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := stringValue(values[key]); text != "" {
			return text
		}
	}
	return ""
}
