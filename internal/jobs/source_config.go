package jobs

import (
	"fmt"
	"net"
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
	ConnectorKind  string
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
	case ingest.SourceTypeMCP:
		spec, err := s.MCPSourceSpec()
		if err != nil {
			return err
		}
		return spec.Validate()
	case "":
		return fmt.Errorf("source %q source_type is required", s.ID)
	default:
		// Non-core source types are accepted as connector overlay configs. The
		// OSS worker only schedules markdown/local_repo/git_repo; overlays own validation.
		return nil
	}
}

type MCPSourceSpec struct {
	ID                  string
	Scope               string
	ServerURL           string
	Tool                string
	Arguments           map[string]any
	BearerTokenEnv      string
	HeaderEnv           map[string]string
	SourceType          string
	AllowPrivateNetwork bool
	AllowScopeExpansion bool
}

func (s SourceConfig) MCPSourceSpec() (MCPSourceSpec, error) {
	serverURL := firstString(s.Config, "server_url", "mcp_url", "url")
	if serverURL == "" {
		serverURL = strings.TrimSpace(s.BaseURL)
	}
	spec := MCPSourceSpec{
		ID:                  s.ID,
		Scope:               strings.TrimSpace(s.Scope),
		ServerURL:           serverURL,
		Tool:                firstString(s.Config, "tool", "tool_name"),
		Arguments:           mapValue(s.Config["arguments"]),
		BearerTokenEnv:      firstString(s.Config, "bearer_token_env", "token_env"),
		HeaderEnv:           stringMapValue(s.Config["header_env"]),
		SourceType:          firstString(s.Config, "document_source_type", "default_source_type"),
		AllowPrivateNetwork: boolValue(s.Config["allow_private_network"]),
		AllowScopeExpansion: boolValue(s.Config["allow_scope_expansion"]),
	}
	if spec.SourceType == "" {
		spec.SourceType = strings.TrimSpace(s.ConnectorKind)
	}
	if spec.SourceType == "" {
		spec.SourceType = string(ingest.SourceTypeMCP)
	}
	return spec, nil
}

func (s MCPSourceSpec) Validate() error {
	if err := s.ValidateServer(); err != nil {
		return err
	}
	if strings.TrimSpace(s.Tool) == "" {
		return fmt.Errorf("source %q mcp tool is required", s.ID)
	}
	return nil
}

func (s MCPSourceSpec) ValidateServer() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("mcp source id is required")
	}
	if strings.TrimSpace(s.Scope) == "" {
		return fmt.Errorf("source %q scope is required", s.ID)
	}
	if strings.TrimSpace(s.ServerURL) == "" {
		return fmt.Errorf("source %q mcp server_url or base_url is required", s.ID)
	}
	u, err := url.Parse(strings.TrimSpace(s.ServerURL))
	if err != nil {
		return fmt.Errorf("parse source %q mcp server_url: %w", s.ID, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("source %q mcp server_url must use http or https", s.ID)
	}
	if u.Host == "" {
		return fmt.Errorf("source %q mcp server_url host is required", s.ID)
	}
	if u.User != nil {
		return fmt.Errorf("source %q mcp server_url must not include user info", s.ID)
	}
	if !s.AllowPrivateNetwork {
		if err := validateMCPPublicHost(s.ID, u.Hostname()); err != nil {
			return err
		}
	}
	return nil
}

func validateMCPPublicHost(sourceID, host string) error {
	host = strings.TrimSpace(strings.TrimSuffix(host, "."))
	if host == "" {
		return fmt.Errorf("source %q mcp server_url host is required", sourceID)
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("source %q mcp server_url points to private host %q; set allow_private_network=true only for trusted local connectors", sourceID, host)
	}
	if ip := net.ParseIP(host); ip != nil && isPrivateMCPIP(ip) {
		return fmt.Errorf("source %q mcp server_url points to private address %q; set allow_private_network=true only for trusted local connectors", sourceID, host)
	}
	return nil
}

func isPrivateMCPIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
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

func mapValue(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func stringMapValue(value any) map[string]string {
	out := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			if strings.TrimSpace(key) != "" && strings.TrimSpace(item) != "" {
				out[strings.TrimSpace(key)] = strings.TrimSpace(item)
			}
		}
	case map[string]any:
		for key, item := range typed {
			text := stringValue(item)
			if strings.TrimSpace(key) != "" && text != "" {
				out[strings.TrimSpace(key)] = text
			}
		}
	}
	return out
}
