package memory

import "github.com/hermawan22/abra/internal/policy"

type RetrievalPlan struct {
	Mode            string                  `json:"mode"`
	Intent          string                  `json:"intent"`
	Budget          RetrievalBudget         `json:"budget"`
	CoverageTargets RetrievalCoverageTarget `json:"coverage_targets"`
	Stages          []RetrievalStage        `json:"stages"`
	StopSignals     []string                `json:"stop_signals"`
}

type RetrievalBudget struct {
	MaxQueries    int `json:"max_queries"`
	Limit         int `json:"limit"`
	ContextTokens int `json:"context_tokens"`
}

type RetrievalStage struct {
	Name              string `json:"name"`
	Purpose           string `json:"purpose"`
	QueryCount        int    `json:"query_count"`
	Limit             int    `json:"limit"`
	IncludeUnverified bool   `json:"include_unverified"`
}

type RetrievalCoverageTarget struct {
	Summaries           int `json:"summaries"`
	Facts               int `json:"facts"`
	SupportingDocuments int `json:"supporting_documents"`
	GraphRelations      int `json:"graph_relations"`
	EvidenceSources     int `json:"evidence_sources"`
}

func buildRetrievalPlan(input ComposeInput, intent string, queries []policy.RecallQuery, graphQueries int) RetrievalPlan {
	if graphQueries < 1 {
		graphQueries = 1
	}
	plan := RetrievalPlan{
		Mode:            retrievalMode(intent),
		Intent:          intent,
		CoverageTargets: retrievalCoverageTarget(intent),
		Budget: RetrievalBudget{
			MaxQueries:    input.MaxQueries,
			Limit:         input.Limit,
			ContextTokens: input.TokenBudget,
		},
		Stages: []RetrievalStage{
			{
				Name:              "hierarchy",
				Purpose:           "load compact repo, module, file, and source summaries before detailed recall",
				QueryCount:        1,
				Limit:             input.Limit,
				IncludeUnverified: input.IncludeUnverified,
			},
			{
				Name:              "source-backed recall",
				Purpose:           "retrieve claims and source chunks scoped to the task",
				QueryCount:        len(queries),
				Limit:             input.Limit,
				IncludeUnverified: input.IncludeUnverified,
			},
			{
				Name:              "graph expansion",
				Purpose:           "expand impacted entities, dependencies, related files, and evidence-backed graph neighbors from task and memory seeds",
				QueryCount:        graphQueries,
				Limit:             input.Limit,
				IncludeUnverified: false,
			},
			{
				Name:              "evidence verification",
				Purpose:           "score source coverage, freshness, and unsafe memory signals before the packet is used",
				QueryCount:        0,
				Limit:             0,
				IncludeUnverified: input.IncludeUnverified,
			},
		},
		StopSignals: []string{
			"enough source-backed facts were found for the task",
			"stale, challenged, or unverified memory requires human or source review",
			"scope-isolated recall returns no relevant context",
		},
	}
	if len(input.Files)+len(input.ChangedFiles) > 0 {
		plan.Stages = append(plan.Stages[:1], append([]RetrievalStage{{
			Name:              "file anchoring",
			Purpose:           "prioritize touched files and nearby ownership, decisions, and validation notes",
			QueryCount:        1,
			Limit:             input.Limit,
			IncludeUnverified: input.IncludeUnverified,
		}}, plan.Stages[1:]...)...)
	}
	return plan
}

func retrievalCoverageTarget(intent string) RetrievalCoverageTarget {
	target := RetrievalCoverageTarget{
		Summaries:           1,
		Facts:               1,
		SupportingDocuments: 1,
		GraphRelations:      1,
		EvidenceSources:     1,
	}
	switch intent {
	case "architecture", "implementation":
		target.Facts = 0
	case "migration":
		target.Facts = 1
		target.GraphRelations = 1
	case "debugging":
		target.Facts = 1
		target.GraphRelations = 1
	}
	return target
}

func retrievalMode(intent string) string {
	switch intent {
	case "migration":
		return "summaries-first + compatibility recall + risk verification"
	case "debugging":
		return "incident recall + graph trace + stale-claim verification"
	case "architecture":
		return "hierarchical overview + graph expansion + evidence verification"
	case "implementation":
		return "file-anchored conventions + reusable context + validation recall"
	default:
		return "scoped summaries + source-backed recall + evidence verification"
	}
}
