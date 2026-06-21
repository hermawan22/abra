package ingest

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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

// Registry stores source definitions by stable source id.
type Registry struct {
	mu      sync.RWMutex
	sources map[string]SourceSpec
}

func NewRegistry(sources ...SourceSpec) (*Registry, error) {
	r := &Registry{sources: make(map[string]SourceSpec, len(sources))}
	for _, source := range sources {
		if err := r.Register(source); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(source SourceSpec) error {
	if err := source.Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[source.ID]; exists {
		return fmt.Errorf("source %q already registered", source.ID)
	}
	r.sources[source.ID] = cloneSource(source)
	return nil
}

func (r *Registry) Get(id string) (SourceSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	source, ok := r.sources[id]
	if !ok {
		return SourceSpec{}, false
	}
	return cloneSource(source), true
}

func (r *Registry) List() []SourceSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sources := make([]SourceSpec, 0, len(r.sources))
	for _, source := range r.sources {
		sources = append(sources, cloneSource(source))
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].ID < sources[j].ID
	})
	return sources
}

func (r *Registry) ListByType(sourceType SourceType) []SourceSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sources []SourceSpec
	for _, source := range r.sources {
		if source.Type == sourceType {
			sources = append(sources, cloneSource(source))
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].ID < sources[j].ID
	})
	return sources
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
