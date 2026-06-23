package graph

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type CandidateSet struct {
	Entities  []EntityCandidate
	Relations []RelationCandidate
}

type EntityCandidate struct {
	Name     string
	Type     string
	Mentions int
}

type RelationCandidate struct {
	From       string
	To         string
	Type       string
	Evidence   string
	SourceID   string
	SourceURL  string
	Confidence float64
}

type sourceText struct {
	ID        string
	SourceURL string
	Text      string
}

var (
	backtickRE         = regexp.MustCompile("`([^`]+)`")
	capitalizedTermRE  = regexp.MustCompile(`\b(?:[A-Z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)?|[A-Z]{2,})(?:[- ][A-Z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)?|[- ][A-Z]{2,}){0,5}\b`)
	sentenceBoundaryRE = regexp.MustCompile(`[!?]\s+|\.\s+|\n+`)
	entityListSplitRE  = regexp.MustCompile(`(?i)\s*(?:,?\s+and\s+|,?\s+or\s+|,\s*)`)
	spaceRE            = regexp.MustCompile(`\s+`)
	leadingArticleRE   = regexp.MustCompile(`(?i)^(?:the|a|an)\s+`)
)

var relationPatterns = []struct {
	relationType     string
	re               *regexp.Regexp
	strictEntityEnds bool
}{
	{
		relationType: "should_not_use",
		re:           regexp.MustCompile(`(?i)(` + entityPattern + `)\s+(?:should|must|shall|needs\s+to|required\s+to)\s+not\s+use\s+(` + entityPattern + listTailPattern + `)`),
	},
	{
		relationType: "should_use",
		re:           regexp.MustCompile(`(?i)(` + entityPattern + `)\s+(?:should|must|shall|needs\s+to|required\s+to)\s+use\s+(` + entityPattern + listTailPattern + `)`),
	},
	{
		relationType: "depends_on",
		re:           regexp.MustCompile(`(?i)(` + entityPattern + `)\s+depends\s+on\s+(` + entityPattern + listTailPattern + `)`),
	},
	{
		relationType: "uses",
		re:           regexp.MustCompile(`(?i)(` + entityPattern + `)\s+uses\s+(` + entityPattern + listTailPattern + `)`),
	},
	{
		relationType: "owns",
		re:           regexp.MustCompile(`(?i)(` + entityPattern + `)\s+owns\s+(` + entityPattern + listTailPattern + `)`),
	},
	{
		relationType:     "supersedes",
		re:               regexp.MustCompile(`(?i)(` + entityPattern + `)\s+(?:supersedes|replaces)\s+(` + entityPattern + `)`),
		strictEntityEnds: true,
	},
	{
		relationType:     "duplicates",
		re:               regexp.MustCompile(`(?i)(` + entityPattern + `)\s+(?:duplicates|is\s+a\s+duplicate\s+of)\s+(` + entityPattern + `)`),
		strictEntityEnds: true,
	},
	{
		relationType:     "derives_from",
		re:               regexp.MustCompile(`(?i)(` + entityPattern + `)\s+derives\s+from\s+(` + entityPattern + `)`),
		strictEntityEnds: true,
	},
}

const (
	backtickedEntityPattern  = "`[^`]+`"
	capitalizedEntityPattern = `(?:[A-Z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)?|[A-Z]{2,})(?:[- ][A-Z][A-Za-z0-9]*(?:\.[A-Za-z0-9]+)?|[- ][A-Z]{2,}){0,5}`
	entityPattern            = `(?:` + backtickedEntityPattern + `|` + capitalizedEntityPattern + `)`
	listTailPattern          = `(?:\s*(?:,|,?\s+and|,?\s+or)\s*` + entityPattern + `)*`
)

var knownTechTerms = map[string]struct{}{
	"airflow":       {},
	"argocd":        {},
	"aws":           {},
	"bigquery":      {},
	"docker":        {},
	"elasticsearch": {},
	"gcp":           {},
	"github":        {},
	"grafana":       {},
	"graphql":       {},
	"helm":          {},
	"k8s":           {},
	"kafka":         {},
	"kubernetes":    {},
	"mongodb":       {},
	"mysql":         {},
	"next.js":       {},
	"node.js":       {},
	"postgres":      {},
	"postgresql":    {},
	"prometheus":    {},
	"react":         {},
	"redis":         {},
	"s3":            {},
	"terraform":     {},
	"typescript":    {},
}

var entitySuffixes = map[string]struct{}{
	"api":      {},
	"app":      {},
	"backend":  {},
	"broker":   {},
	"cluster":  {},
	"consumer": {},
	"database": {},
	"db":       {},
	"frontend": {},
	"gateway":  {},
	"group":    {},
	"job":      {},
	"model":    {},
	"pipeline": {},
	"platform": {},
	"producer": {},
	"queue":    {},
	"service":  {},
	"stream":   {},
	"system":   {},
	"team":     {},
	"topic":    {},
	"worker":   {},
	"workflow": {},
}

var ignoredEntityPrefixes = map[string]struct{}{
	"the": {},
	"a":   {},
	"an":  {},
}

// ExtractFromClaims returns deterministic entity and relation candidates from claim text.
func ExtractFromClaims(claims []string) CandidateSet {
	texts := make([]sourceText, 0, len(claims))
	for _, claim := range claims {
		texts = append(texts, sourceText{Text: claim})
	}
	return extract(texts)
}

func extract(texts []sourceText) CandidateSet {
	entities := map[string]*EntityCandidate{}
	relations := map[string]RelationCandidate{}

	for _, source := range texts {
		for _, name := range extractEntityNames(source.Text) {
			addEntity(entities, name)
		}
		for _, relation := range extractRelations(source) {
			addEntity(entities, relation.From)
			addEntity(entities, relation.To)
			key := relation.From + "\x00" + relation.Type + "\x00" + relation.To
			if _, exists := relations[key]; !exists {
				relations[key] = relation
			}
		}
	}

	out := CandidateSet{
		Entities:  make([]EntityCandidate, 0, len(entities)),
		Relations: make([]RelationCandidate, 0, len(relations)),
	}
	for _, entity := range entities {
		out.Entities = append(out.Entities, *entity)
	}
	for _, relation := range relations {
		out.Relations = append(out.Relations, relation)
	}

	sort.Slice(out.Entities, func(a, b int) bool {
		return strings.ToLower(out.Entities[a].Name) < strings.ToLower(out.Entities[b].Name)
	})
	sort.Slice(out.Relations, func(a, b int) bool {
		left := relationSortKey(out.Relations[a])
		right := relationSortKey(out.Relations[b])
		return left < right
	})
	return out
}

func extractEntityNames(text string) []string {
	seen := map[string]string{}
	for _, match := range backtickRE.FindAllStringSubmatch(text, -1) {
		if name := canonicalEntityName(match[1]); name != "" {
			seen[strings.ToLower(name)] = name
		}
	}
	for _, match := range capitalizedTermRE.FindAllString(text, -1) {
		name := canonicalEntityName(match)
		if name != "" && looksLikeEntity(name) {
			seen[strings.ToLower(name)] = name
		}
	}

	names := make([]string, 0, len(seen))
	for _, name := range seen {
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool {
		return strings.ToLower(names[a]) < strings.ToLower(names[b])
	})
	return names
}

func extractRelations(source sourceText) []RelationCandidate {
	var relations []RelationCandidate
	for _, sentence := range splitSentences(source.Text) {
		for _, pattern := range relationPatterns {
			for _, match := range pattern.re.FindAllStringSubmatch(sentence, -1) {
				if len(match) < 3 {
					continue
				}
				from := canonicalEntityName(match[1])
				if from == "" {
					continue
				}
				if pattern.strictEntityEnds && !looksLikeEntity(from) {
					continue
				}
				for _, to := range splitEntityList(match[2]) {
					if to == "" || strings.EqualFold(from, to) {
						continue
					}
					if pattern.strictEntityEnds && !looksLikeEntity(to) {
						continue
					}
					relations = append(relations, RelationCandidate{
						From:       from,
						To:         to,
						Type:       pattern.relationType,
						Evidence:   cleanEvidence(sentence),
						SourceID:   source.ID,
						SourceURL:  source.SourceURL,
						Confidence: 0.7,
					})
				}
			}
		}
	}
	return relations
}

func splitSentences(text string) []string {
	parts := sentenceBoundaryRE.Split(text, -1)
	sentences := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			sentences = append(sentences, part)
		}
	}
	return sentences
}

func splitEntityList(value string) []string {
	value = strings.ReplaceAll(value, "`", "")
	parts := entityListSplitRE.Split(value, -1)
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := canonicalEntityName(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func addEntity(entities map[string]*EntityCandidate, name string) {
	name = canonicalEntityName(name)
	if name == "" {
		return
	}
	key := strings.ToLower(name)
	if entity, exists := entities[key]; exists {
		entity.Mentions++
		return
	}
	entities[key] = &EntityCandidate{
		Name:     name,
		Type:     inferEntityType(name),
		Mentions: 1,
	}
}

func canonicalEntityName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	value = strings.Trim(value, " \t\r\n,;:()[]{}\"'")
	value = leadingArticleRE.ReplaceAllString(value, "")
	value = spaceRE.ReplaceAllString(value, " ")
	words := strings.Fields(value)
	for len(words) > 0 {
		if _, ignored := ignoredEntityPrefixes[strings.ToLower(words[0])]; !ignored {
			break
		}
		words = words[1:]
	}
	value = strings.Join(words, " ")
	if len(value) < 2 || strings.Contains(value, "\n") {
		return ""
	}
	return value
}

func looksLikeEntity(name string) bool {
	words := strings.Fields(strings.ReplaceAll(name, "-", " "))
	if len(words) == 0 {
		return false
	}
	lower := strings.ToLower(name)
	if _, ok := knownTechTerms[lower]; ok {
		return true
	}
	last := strings.ToLower(words[len(words)-1])
	if _, ok := entitySuffixes[last]; ok {
		return true
	}
	if isAcronym(name) {
		return true
	}
	return len(words) > 1 && containsEntityWord(words)
}

func containsEntityWord(words []string) bool {
	for _, word := range words {
		if _, ok := entitySuffixes[strings.ToLower(word)]; ok {
			return true
		}
		if _, ok := knownTechTerms[strings.ToLower(word)]; ok {
			return true
		}
	}
	return false
}

func inferEntityType(name string) string {
	lower := strings.ToLower(name)
	words := strings.Fields(strings.ReplaceAll(lower, "-", " "))
	last := ""
	if len(words) > 0 {
		last = words[len(words)-1]
	}
	switch {
	case last == "team" || last == "group":
		return "team"
	case last == "service" || last == "api" || last == "app" || last == "backend" || last == "frontend" || last == "gateway" || last == "worker":
		return "service"
	case last == "database" || last == "db" || last == "queue" || last == "topic" || last == "cluster" || last == "platform" || last == "pipeline" || last == "system":
		return "system"
	case isKnownTechnology(lower):
		return "technology"
	default:
		return "unknown"
	}
}

func isKnownTechnology(lower string) bool {
	if _, ok := knownTechTerms[lower]; ok {
		return true
	}
	for _, word := range strings.Fields(strings.ReplaceAll(lower, "-", " ")) {
		if _, ok := knownTechTerms[word]; ok {
			return true
		}
	}
	return false
}

func isAcronym(value string) bool {
	letters := 0
	for _, r := range value {
		if unicode.IsLetter(r) {
			letters++
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return letters >= 2
}

func cleanEvidence(value string) string {
	return strings.Trim(strings.TrimSpace(spaceRE.ReplaceAllString(value, " ")), ".!?")
}

func relationSortKey(relation RelationCandidate) string {
	return strings.ToLower(relation.From) + "\x00" + relation.Type + "\x00" + strings.ToLower(relation.To)
}
