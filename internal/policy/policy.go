package policy

import (
	"strings"
)

type Hook string

const (
	HookBeforeTask Hook = "before_task"
	HookBeforeCode Hook = "before_code"
	HookAfterTask  Hook = "after_task"
)

type Event struct {
	Hook         Hook     `json:"hook"`
	Task         string   `json:"task"`
	Scope        string   `json:"scope,omitempty"`
	Files        []string `json:"files,omitempty"`
	Language     string   `json:"language,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

type RecallQuery struct {
	Query             string `json:"query"`
	Scope             string `json:"scope"`
	Limit             int    `json:"limit"`
	IncludeUnverified bool   `json:"include_unverified"`
	Reason            string `json:"reason"`
}

type RecallPlan struct {
	Hook     Hook          `json:"hook"`
	Required bool          `json:"required"`
	Queries  []RecallQuery `json:"queries"`
}

type Engine struct {
	Config Config
}

type Config struct {
	DefaultScope      string
	DefaultLimit      int
	IncludeUnverified bool
	MaxQueries        int
}

func NewEngine(config Config) *Engine {
	if strings.TrimSpace(config.DefaultScope) == "" {
		config.DefaultScope = "company"
	}
	if config.DefaultLimit <= 0 {
		config.DefaultLimit = 5
	}
	if config.MaxQueries <= 0 {
		config.MaxQueries = 4
	}
	return &Engine{Config: config}
}

func (e *Engine) Plan(event Event) RecallPlan {
	scope := strings.TrimSpace(event.Scope)
	if scope == "" {
		scope = e.Config.DefaultScope
	}

	var queries []RecallQuery
	switch event.Hook {
	case HookBeforeTask:
		queries = e.beforeTask(event, scope)
	case HookBeforeCode:
		queries = e.beforeCode(event, scope)
	case HookAfterTask:
		queries = e.afterTask(event, scope)
	default:
		queries = nil
	}

	if len(queries) > e.Config.MaxQueries {
		queries = queries[:e.Config.MaxQueries]
	}
	return RecallPlan{
		Hook:     event.Hook,
		Required: len(queries) > 0,
		Queries:  queries,
	}
}

func (e *Engine) beforeTask(event Event, scope string) []RecallQuery {
	task := compact(event.Task)
	if task == "" {
		return nil
	}
	return []RecallQuery{
		e.query(scope, "Project decisions, constraints, and source-backed facts relevant to: "+task, "establish task context before acting"),
		e.query(scope, "Known risks, stale assumptions, or disputed claims related to: "+task, "avoid using unsafe memory silently"),
	}
}

func (e *Engine) beforeCode(event Event, scope string) []RecallQuery {
	task := compact(event.Task)
	files := compact(strings.Join(append([]string{}, event.Files...), " "))
	changed := compact(strings.Join(append([]string{}, event.ChangedFiles...), " "))
	language := compact(event.Language)

	var queries []RecallQuery
	if task != "" {
		queries = append(queries, e.query(scope, "Coding conventions and implementation constraints for: "+task, "align code changes to known project practice"))
	}
	if files != "" {
		queries = append(queries, e.query(scope, "Prior decisions and ownership notes for files: "+files, "retrieve file-specific context before editing"))
	}
	if changed != "" && changed != files {
		queries = append(queries, e.query(scope, "Validation notes and previous failures for changed files: "+changed, "focus checks on touched surfaces"))
	}
	if language != "" {
		queries = append(queries, e.query(scope, "Language and framework standards for "+language, "recall stack-specific standards"))
	}
	return dedupeQueries(queries)
}

func (e *Engine) afterTask(event Event, scope string) []RecallQuery {
	task := compact(event.Task)
	files := compact(strings.Join(event.ChangedFiles, " "))
	if files == "" {
		files = compact(strings.Join(event.Files, " "))
	}

	var queries []RecallQuery
	if task != "" {
		queries = append(queries, e.query(scope, "Acceptance criteria and verification expectations for: "+task, "check completion against recalled requirements"))
		queries = append(queries, e.query(scope, "Corrections, challenges, or follow-up decisions related to: "+task, "detect memory updates needed after work"))
	}
	if files != "" {
		queries = append(queries, e.query(scope, "Regression risks and test guidance for changed files: "+files, "select final validation targets"))
	}
	return dedupeQueries(queries)
}

func (e *Engine) query(scope, query, reason string) RecallQuery {
	return RecallQuery{
		Query:             query,
		Scope:             scope,
		Limit:             e.Config.DefaultLimit,
		IncludeUnverified: e.Config.IncludeUnverified,
		Reason:            reason,
	}
}

func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func dedupeQueries(in []RecallQuery) []RecallQuery {
	seen := make(map[string]struct{}, len(in))
	out := make([]RecallQuery, 0, len(in))
	for _, q := range in {
		key := q.Scope + "\x00" + q.Query
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, q)
	}
	return out
}
