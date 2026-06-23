package memory

import (
	"path/filepath"
	"sort"
	"strings"
)

func validationPlan(input ComposeInput, result ComposeResult) []ValidationStep {
	targets := validationTargets(input, result)
	steps := []ValidationStep{}
	add := func(step ValidationStep) {
		step.Name = strings.TrimSpace(step.Name)
		step.Type = strings.TrimSpace(step.Type)
		step.Command = strings.TrimSpace(step.Command)
		step.Reason = strings.TrimSpace(step.Reason)
		step.Targets = compactList(step.Targets)
		if step.Name == "" || step.Type == "" || step.Reason == "" {
			return
		}
		for _, existing := range steps {
			if existing.Type == step.Type && existing.Command == step.Command && existing.Name == step.Name {
				return
			}
		}
		steps = append(steps, step)
	}

	if result.Verification.ActionRequired || result.AgentDecision.ReviewRequired {
		add(ValidationStep{
			Name:     "Review memory gate",
			Type:     "memory_gate",
			Reason:   "Verification, memory health, or stored agent policy requires review before autonomous work.",
			Targets:  gateTargets(result),
			Priority: 1,
			Required: true,
		})
	}
	if touchesGo(input, targets) {
		add(ValidationStep{
			Name:     "Run Go tests",
			Type:     "test",
			Command:  "go test ./...",
			Reason:   "Go files or Go service areas appear in the task impact set.",
			Targets:  filterTargets(targets, goTarget),
			Priority: 2,
			Required: true,
		})
	}
	if touchesJavaScript(input, targets) {
		add(ValidationStep{
			Name:     "Run package tests",
			Type:     "test",
			Command:  "npm test",
			Reason:   "JavaScript or TypeScript files appear in the task impact set.",
			Targets:  filterTargets(targets, jsTarget),
			Priority: 2,
			Required: true,
		})
		if result.Intent == "migration" || result.Intent == "implementation" {
			add(ValidationStep{
				Name:     "Run package build",
				Type:     "build",
				Command:  "npm run build",
				Reason:   "Implementation or migration work should verify frontend/package build compatibility when a build script exists.",
				Targets:  filterTargets(targets, jsTarget),
				Priority: 3,
				Required: false,
			})
		}
	}
	if touchesDocker(targets) {
		add(ValidationStep{
			Name:     "Validate Docker Compose config",
			Type:     "config",
			Command:  "docker compose config",
			Reason:   "Docker Compose files appear in the task impact set.",
			Targets:  filterTargets(targets, dockerTarget),
			Priority: 3,
			Required: true,
		})
	}
	if touchesHelm(targets) {
		add(ValidationStep{
			Name:     "Render Helm chart",
			Type:     "config",
			Command:  "helm template abra deploy/helm",
			Reason:   "Helm chart files appear in the task impact set.",
			Targets:  filterTargets(targets, helmTarget),
			Priority: 3,
			Required: true,
		})
	}
	if len(steps) == 0 {
		add(ValidationStep{
			Name:     "Source review",
			Type:     "review",
			Reason:   "No deterministic test command was inferred; inspect cited evidence and impacted files before acting.",
			Targets:  targets,
			Priority: 4,
			Required: true,
		})
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Priority == steps[j].Priority {
			return steps[i].Name < steps[j].Name
		}
		return steps[i].Priority < steps[j].Priority
	})
	if len(steps) > 8 {
		return steps[:8]
	}
	return steps
}

func validationTargets(input ComposeInput, result ComposeResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range append(input.Files, input.ChangedFiles...) {
		add(value)
	}
	for _, value := range result.RelevantFiles {
		add(value)
	}
	for _, item := range result.ImpactMap {
		if item.Kind == "file" || item.Kind == "module" || item.Kind == "repo" {
			add(item.Name)
		}
	}
	sort.Strings(out)
	if len(out) > 30 {
		return out[:30]
	}
	return out
}

func gateTargets(result ComposeResult) []string {
	targets := []string{}
	targets = append(targets, result.Verification.UnverifiedClaims...)
	targets = append(targets, result.Verification.StaleClaims...)
	targets = append(targets, result.Verification.ChallengedClaims...)
	targets = append(targets, result.Verification.MissingEvidenceClaims...)
	targets = append(targets, result.Verification.ConflictClaims...)
	for _, conflict := range result.Conflicts {
		targets = append(targets, conflict.ID)
	}
	for _, warning := range result.GraphWarnings {
		targets = append(targets, warning.Entity)
		for _, relation := range warning.Relations {
			targets = append(targets, relation.FromEntity, relation.ToEntity)
		}
	}
	return compactList(targets)
}

func touchesGo(input ComposeInput, targets []string) bool {
	if strings.Contains(strings.ToLower(input.Language), "go") {
		return true
	}
	for _, target := range targets {
		if goTarget(target) {
			return true
		}
	}
	return false
}

func touchesJavaScript(input ComposeInput, targets []string) bool {
	language := strings.ToLower(input.Language)
	if strings.Contains(language, "javascript") || strings.Contains(language, "typescript") || strings.Contains(language, "react") || strings.Contains(language, "node") {
		return true
	}
	for _, target := range targets {
		if jsTarget(target) {
			return true
		}
	}
	return false
}

func touchesDocker(targets []string) bool {
	for _, target := range targets {
		if dockerTarget(target) {
			return true
		}
	}
	return false
}

func touchesHelm(targets []string) bool {
	for _, target := range targets {
		if helmTarget(target) {
			return true
		}
	}
	return false
}

func filterTargets(targets []string, predicate func(string) bool) []string {
	out := []string{}
	for _, target := range targets {
		if predicate(target) {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func goTarget(target string) bool {
	return strings.HasSuffix(target, ".go") || target == "go.mod" || target == "go.sum" || strings.HasPrefix(target, "cmd/") || strings.HasPrefix(target, "internal/")
}

func jsTarget(target string) bool {
	switch filepath.Ext(target) {
	case ".js", ".jsx", ".ts", ".tsx", ".json":
		return true
	default:
		return target == "package.json" || strings.HasPrefix(target, "frontend/") || strings.HasPrefix(target, "src/")
	}
}

func dockerTarget(target string) bool {
	base := strings.ToLower(filepath.Base(target))
	return base == "dockerfile" || strings.HasPrefix(base, "docker-compose") || strings.Contains(base, "compose.")
}

func helmTarget(target string) bool {
	return strings.HasPrefix(target, "deploy/helm/") || strings.HasSuffix(target, "Chart.yaml")
}

func suggestedSteps(input ComposeInput, intent string, result ComposeResult) []string {
	steps := []string{"Review the top facts and evidence sources before changing behavior."}
	switch intent {
	case "migration":
		steps = append(steps, "Check package/runtime compatibility and identify dependency blockers before bumping versions.")
	case "debugging":
		steps = append(steps, "Trace the highest-confidence graph relations and inspect the most relevant files first.")
	case "architecture":
		steps = append(steps, "Use hierarchical summaries for the global answer, then cite specific facts and source chunks.")
	default:
		steps = append(steps, "Use the relevant files list as the initial edit/read set, then expand through graph relations.")
	}
	if len(result.Risks) > 0 && !strings.HasPrefix(result.Risks[0], "No stale") {
		steps = append(steps, "Resolve stale, challenged, or unverified memory before presenting conclusions as facts.")
	}
	if result.Verification.ActionRequired {
		steps = append(steps, "Treat the verification report as a gate before using this packet for autonomous changes.")
	}
	if len(result.Verification.WeakEvidenceAnchors) > 0 {
		steps = append(steps, "Create a learning proposal to attach text-span evidence before asking for synthesized answers.")
	}
	if result.MemoryHealth.Status != "" && result.MemoryHealth.Status != "healthy" {
		steps = append(steps, "Inspect memory health signals before using this packet for autonomous changes.")
	}
	if len(input.ChangedFiles) > 0 {
		steps = append(steps, "Run focused validation for changed files and compare against recalled verification expectations.")
	}
	return steps
}
