package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/ai"
)

type SynthesisResult struct {
	Enabled      bool      `json:"enabled"`
	Status       string    `json:"status"`
	Provider     string    `json:"provider,omitempty"`
	Model        string    `json:"model,omitempty"`
	Answer       string    `json:"answer,omitempty"`
	CitationRefs []string  `json:"citation_refs,omitempty"`
	Warning      string    `json:"warning,omitempty"`
	Usage        *ai.Usage `json:"usage,omitempty"`
}

type SynthesisInput struct {
	Question            string
	Scope               string
	DeterministicAnswer string
	Citations           []Citation
	EvidenceAnchors     []EvidenceAnchor
	Verification        VerificationReport
	AgentDecision       AgentDecision
	MaxTokens           int
}

type Synthesizer interface {
	Synthesize(ctx context.Context, input SynthesisInput) (SynthesisResult, error)
}

type ExtractorSynthesizer struct {
	provider  ai.ExtractorProvider
	model     string
	maxTokens int
}

func NewExtractorSynthesizer(provider ai.ExtractorProvider, model string, maxTokens int) *ExtractorSynthesizer {
	if maxTokens <= 0 {
		maxTokens = 700
	}
	return &ExtractorSynthesizer{provider: provider, model: strings.TrimSpace(model), maxTokens: maxTokens}
}

func (s *ExtractorSynthesizer) Synthesize(ctx context.Context, input SynthesisInput) (SynthesisResult, error) {
	if s == nil || s.provider == nil {
		return SynthesisResult{Enabled: false, Status: "disabled", Warning: "synthesis provider is not configured"}, nil
	}
	if input.Verification.Verdict == "unsafe" || input.AgentDecision.ReviewRequired {
		return SynthesisResult{Enabled: true, Status: "blocked", Warning: "synthesis requires a non-unsafe verification verdict and no review-required gate"}, nil
	}
	if len(input.Citations) == 0 || len(input.EvidenceAnchors) == 0 {
		return SynthesisResult{Enabled: true, Status: "blocked", Warning: "synthesis requires citations and evidence anchors"}, nil
	}
	maxTokens := input.MaxTokens
	if maxTokens <= 0 {
		maxTokens = s.maxTokens
	}
	response, err := s.provider.Extract(ctx, ai.ExtractionRequest{
		Input:        synthesisPrompt(input),
		Instructions: synthesisInstructions(),
		Model:        s.model,
		SchemaName:   "abra_brain_synthesis",
		Schema:       synthesisSchema(),
		MaxTokens:    maxTokens,
		Metadata: map[string]any{
			"operation": "brain_synthesis",
			"scope":     input.Scope,
		},
	})
	if err != nil {
		return SynthesisResult{Enabled: true, Status: "error", Warning: err.Error()}, nil
	}
	value, _ := response.Value.(map[string]any)
	answer := strings.TrimSpace(stringFromAny(value["answer"]))
	refs := stringsFromAny(value["citation_refs"])
	if answer == "" {
		return SynthesisResult{Enabled: true, Status: "rejected", Provider: response.Provider, Model: response.Model, Warning: "synthesis response did not include an answer", Usage: response.Usage}, nil
	}
	validRefs := citationRefSet(input.Citations)
	if len(refs) == 0 {
		return SynthesisResult{Enabled: true, Status: "rejected", Provider: response.Provider, Model: response.Model, Warning: "synthesis must include at least one citation ref", Usage: response.Usage}, nil
	}
	for _, ref := range refs {
		if _, ok := validRefs[ref]; !ok {
			return SynthesisResult{Enabled: true, Status: "rejected", Provider: response.Provider, Model: response.Model, Warning: "synthesis cited evidence outside the retrieval packet", Usage: response.Usage}, nil
		}
		if !strings.Contains(answer, "["+ref+"]") {
			return SynthesisResult{Enabled: true, Status: "rejected", Provider: response.Provider, Model: response.Model, Warning: "synthesis citation refs must appear inline in the answer", Usage: response.Usage}, nil
		}
	}
	return SynthesisResult{
		Enabled:      true,
		Status:       "ok",
		Provider:     response.Provider,
		Model:        response.Model,
		Answer:       answer,
		CitationRefs: refs,
		Usage:        response.Usage,
	}, nil
}

func (c *Composer) synthesizeThink(ctx context.Context, input ThinkInput, result ThinkResult) ThinkResult {
	if c == nil || c.synthesizer == nil {
		result.Synthesis = SynthesisResult{Enabled: false, Status: "disabled", Warning: "synthesis provider is not configured"}
		return result
	}
	synthesized, err := c.synthesizer.Synthesize(ctx, SynthesisInput{
		Question:            input.Question,
		Scope:               input.Scope,
		DeterministicAnswer: result.DeterministicAnswer,
		Citations:           result.Citations,
		EvidenceAnchors:     result.EvidenceAnchors,
		Verification:        result.Verification,
		AgentDecision:       result.AgentDecision,
		MaxTokens:           input.TokenBudget,
	})
	if err != nil {
		result.Synthesis = SynthesisResult{Enabled: true, Status: "error", Warning: err.Error()}
		return result
	}
	result.Synthesis = synthesized
	if synthesized.Status == "ok" && strings.TrimSpace(synthesized.Answer) != "" {
		result.Answer = synthesized.Answer
	}
	return result
}

func synthesisInstructions() string {
	return strings.Join([]string{
		"You synthesize Abra governed memory into a concise answer.",
		"Use only the provided deterministic answer, citations, and evidence anchors.",
		"Every factual sentence must include an inline citation like [C1].",
		"Do not add facts that are absent from the evidence anchors.",
		"Return only JSON matching the schema.",
	}, "\n")
}

func synthesisPrompt(input SynthesisInput) string {
	payload := map[string]any{
		"question":             input.Question,
		"scope":                input.Scope,
		"deterministic_answer": input.DeterministicAnswer,
		"verification": map[string]any{
			"verdict":          input.Verification.Verdict,
			"score":            input.Verification.Score,
			"required_actions": input.Verification.RequiredActions,
			"recommendations":  input.Verification.Recommendations,
		},
		"agent_decision": map[string]any{
			"decision":             input.AgentDecision.Decision,
			"autonomous_allowed":   input.AgentDecision.AutonomousAllowed,
			"review_required":      input.AgentDecision.ReviewRequired,
			"allowed_next_actions": input.AgentDecision.AllowedNextActions,
		},
		"citations":        input.Citations,
		"evidence_anchors": input.EvidenceAnchors,
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	return string(raw)
}

func synthesisSchema() ai.JSONSchema {
	return ai.JSONSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"answer", "citation_refs"},
		"properties": map[string]any{
			"answer": map[string]any{
				"type":        "string",
				"description": "Concise answer with inline citation refs like [C1].",
			},
			"citation_refs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

func citationRefSet(citations []Citation) map[string]struct{} {
	out := map[string]struct{}{}
	for _, citation := range citations {
		ref := strings.TrimSpace(citation.Ref)
		if ref != "" {
			out[ref] = struct{}{}
		}
	}
	return out
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func stringsFromAny(value any) []string {
	raw, _ := value.([]any)
	out := []string{}
	for _, item := range raw {
		text := strings.TrimSpace(stringFromAny(item))
		if text != "" {
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return appendUnique(out)
}
