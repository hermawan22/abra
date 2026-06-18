package memory

import (
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type GraphWarning struct {
	WarningType  string                 `json:"warning_type"`
	Severity     string                 `json:"severity"`
	Entity       string                 `json:"entity"`
	RelationType string                 `json:"relation_type"`
	Message      string                 `json:"message"`
	Relations    []GraphWarningRelation `json:"relations"`
}

type GraphWarningRelation struct {
	ID         string  `json:"id,omitempty"`
	FromEntity string  `json:"from_entity"`
	ToEntity   string  `json:"to_entity"`
	Type       string  `json:"relation_type"`
	Confidence float64 `json:"confidence"`
	SourceURL  string  `json:"source_url,omitempty"`
}

var exclusiveGraphAlternatives = map[string]string{
	"playwright":  "browser_test_runner",
	"cypress":     "browser_test_runner",
	"selenium":    "browser_test_runner",
	"webdriverio": "browser_test_runner",
	"testcafe":    "browser_test_runner",
}

func graphWarnings(relations []store.RelationResult) []GraphWarning {
	if len(relations) == 0 {
		return nil
	}
	warnings := map[string]GraphWarning{}
	byFrom := map[string][]store.RelationResult{}
	for _, relation := range relations {
		from := normalizeGraphTerm(relation.FromEntity)
		to := normalizeGraphTerm(relation.ToEntity)
		if from == "" || to == "" {
			continue
		}
		byFrom[from] = append(byFrom[from], relation)
	}

	for _, group := range byFrom {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				left := group[i]
				right := group[j]
				if warning, ok := graphRelationWarning(left, right); ok {
					warnings[graphWarningKey(warning)] = warning
				}
			}
		}
	}

	out := make([]GraphWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, warning)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if severityRank(out[i].Severity) == severityRank(out[j].Severity) {
			return graphWarningKey(out[i]) < graphWarningKey(out[j])
		}
		return severityRank(out[i].Severity) > severityRank(out[j].Severity)
	})
	if len(out) > 10 {
		return out[:10]
	}
	return out
}

func graphRelationWarning(left, right store.RelationResult) (GraphWarning, bool) {
	leftType := normalizeGraphTerm(left.Type)
	rightType := normalizeGraphTerm(right.Type)
	leftTo := normalizeGraphTerm(left.ToEntity)
	rightTo := normalizeGraphTerm(right.ToEntity)
	if leftTo == "" || rightTo == "" {
		return GraphWarning{}, false
	}

	if sameTargetOppositePolicy(leftType, rightType, leftTo, rightTo) {
		return newGraphWarning("opposing_graph_policy", "high", left.FromEntity, left.Type, "Graph context says the same entity should both use and not use "+left.ToEntity+".", left, right), true
	}

	leftGroup := exclusiveGraphAlternatives[leftTo]
	rightGroup := exclusiveGraphAlternatives[rightTo]
	if leftGroup != "" && leftGroup == rightGroup && leftTo != rightTo && isPreferredUseRelation(leftType) && isPreferredUseRelation(rightType) {
		severity := "medium"
		if leftType == "should_use" || rightType == "should_use" {
			severity = "high"
		}
		return newGraphWarning("competing_graph_alternatives", severity, left.FromEntity, left.Type, left.FromEntity+" has competing "+leftGroup+" graph relations: "+left.ToEntity+" and "+right.ToEntity+".", left, right), true
	}

	return GraphWarning{}, false
}

func sameTargetOppositePolicy(leftType, rightType, leftTo, rightTo string) bool {
	if leftTo != rightTo {
		return false
	}
	return (leftType == "should_not_use" && isPositiveUseRelation(rightType)) || (rightType == "should_not_use" && isPositiveUseRelation(leftType))
}

func isPositiveUseRelation(value string) bool {
	switch value {
	case "should_use", "uses", "depends_on":
		return true
	default:
		return false
	}
}

func isPreferredUseRelation(value string) bool {
	switch value {
	case "should_use", "uses":
		return true
	default:
		return false
	}
}

func newGraphWarning(warningType, severity, entity, relationType, message string, relations ...store.RelationResult) GraphWarning {
	items := make([]GraphWarningRelation, 0, len(relations))
	for _, relation := range relations {
		sourceURL := ""
		if relation.SourceURL != nil {
			sourceURL = *relation.SourceURL
		}
		items = append(items, GraphWarningRelation{
			ID:         relation.ID,
			FromEntity: relation.FromEntity,
			ToEntity:   relation.ToEntity,
			Type:       relation.Type,
			Confidence: relation.Confidence,
			SourceURL:  sourceURL,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := strings.ToLower(items[i].Type + "\x00" + items[i].FromEntity + "\x00" + items[i].ToEntity + "\x00" + items[i].ID)
		right := strings.ToLower(items[j].Type + "\x00" + items[j].FromEntity + "\x00" + items[j].ToEntity + "\x00" + items[j].ID)
		return left < right
	})
	return GraphWarning{
		WarningType:  warningType,
		Severity:     severity,
		Entity:       entity,
		RelationType: relationType,
		Message:      message,
		Relations:    items,
	}
}

func graphWarningKey(warning GraphWarning) string {
	parts := []string{warning.WarningType, warning.Severity, warning.Entity, warning.RelationType}
	for _, relation := range warning.Relations {
		parts = append(parts, relation.Type, relation.FromEntity, relation.ToEntity, relation.ID)
	}
	return strings.ToLower(strings.Join(parts, "\x00"))
}

func normalizeGraphTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, " \t\r\n\"'`.,;:()[]{}")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func severityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "blocking":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
