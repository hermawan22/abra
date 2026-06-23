package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hermawan22/abra/internal/graph"
)

func codeGraphPath(input IngestDocumentInput) string {
	for _, key := range []string{"git_path", "ingest_path", "path", "source_path", "repo_path"} {
		if value := metadataString(input.Metadata, key); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func checksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func chunkText(content string, maxChars int) []string {
	if maxChars < 1 {
		maxChars = 1200
	}
	parts := regexp.MustCompile(`\n{2,}`).Split(content, -1)
	var chunks []string
	current := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, piece := range splitOversizedPart(part, maxChars, minInt(120, maxChars/5)) {
			next := strings.TrimSpace(current + "\n\n" + piece)
			if len(next) > maxChars && current != "" {
				chunks = append(chunks, strings.TrimSpace(current))
				current = piece
				continue
			}
			current = next
		}
	}
	if strings.TrimSpace(current) != "" {
		chunks = append(chunks, strings.TrimSpace(current))
	}
	return chunks
}

func splitOversizedPart(part string, maxChars, overlap int) []string {
	part = strings.TrimSpace(part)
	if part == "" {
		return nil
	}
	if len(part) <= maxChars {
		return []string{part}
	}
	lines := strings.Split(part, "\n")
	if len(lines) > 1 {
		pieces := []string{}
		current := ""
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(line) > maxChars {
				if current != "" {
					pieces = append(pieces, current)
					current = ""
				}
				pieces = append(pieces, hardSplitText(line, maxChars, overlap)...)
				continue
			}
			next := strings.TrimSpace(current + "\n" + line)
			if len(next) > maxChars && current != "" {
				pieces = append(pieces, current)
				current = line
				continue
			}
			current = next
		}
		if current != "" {
			pieces = append(pieces, current)
		}
		return pieces
	}
	return hardSplitText(part, maxChars, overlap)
}

func hardSplitText(value string, maxChars, overlap int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if maxChars < 1 {
		maxChars = 1200
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 5
	}
	pieces := []string{}
	for start := 0; start < len(value); {
		end := utf8BoundaryAtOrBefore(value, minInt(start+maxChars, len(value)), start)
		if end <= start {
			end = minInt(start+maxChars, len(value))
		}
		piece := strings.TrimSpace(value[start:end])
		if piece != "" {
			pieces = append(pieces, piece)
		}
		if end >= len(value) {
			break
		}
		nextStart := end - overlap
		if nextStart <= start {
			nextStart = end
		}
		start = utf8BoundaryAtOrBefore(value, nextStart, 0)
		if start < 0 || start >= end {
			start = end
		}
	}
	return pieces
}

func utf8BoundaryAtOrBefore(value string, index, minIndex int) int {
	if index >= len(value) {
		return len(value)
	}
	if index < minIndex {
		return minIndex
	}
	for index > minIndex && !utf8.RuneStart(value[index]) {
		index--
	}
	return index
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func extractClaimsForDocument(input IngestDocumentInput, content string) []string {
	contentKind := metadataString(input.Metadata, "content_kind")
	codePath := codeGraphPath(input)
	if contentKind == "code" || (codePath != "" && graph.IsCodeGraphPath(codePath)) {
		return nil
	}
	return extractClaims(stripFencedCodeBlocks(content))
}

func extractClaims(content string) []string {
	candidates := map[string]struct{}{}
	proseLines := []string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			claim := cleanClaim(line[2:])
			if isExtractableClaim(claim) {
				candidates[claim] = struct{}{}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if line != "" {
			proseLines = append(proseLines, line)
		}
	}
	sentences := regexp.MustCompile(`(?m)([A-Z][^.!?]{39,260}[.!?])`).FindAllString(strings.Join(proseLines, "\n"), -1)
	keywords := regexp.MustCompile(`(?i)\b(should|must|required|default|standard|use|uses|prefer|avoid|deprecated|supersedes|replaces|duplicates|derives)\b`)
	for _, sentence := range sentences {
		claim := cleanClaim(sentence)
		if keywords.MatchString(claim) && isExtractableClaim(claim) {
			candidates[claim] = struct{}{}
		}
	}
	claims := make([]string, 0, len(candidates))
	for claim := range candidates {
		claims = append(claims, claim)
	}
	sort.Strings(claims)
	if len(claims) > 25 {
		claims = claims[:25]
	}
	return claims
}

func cleanClaim(value string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(value, " "))
}

func stripFencedCodeBlocks(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isExtractableClaim(claim string) bool {
	claim = strings.TrimSpace(claim)
	if len(claim) < 20 || len(claim) > 260 {
		return false
	}
	if looksLikeCodeClaim(claim) {
		return false
	}
	return true
}

func looksLikeCodeClaim(claim string) bool {
	lower := strings.ToLower(strings.TrimSpace(claim))
	codePrefixes := []string{
		"case ",
		"const ",
		"else ",
		"export ",
		"for ",
		"func ",
		"function ",
		"if ",
		"import ",
		"insert ",
		"let ",
		"return ",
		"select ",
		"switch ",
		"type ",
		"update ",
		"var ",
		"where ",
	}
	for _, prefix := range codePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.Contains(claim, " := ") || strings.Contains(claim, " => ") || strings.Contains(claim, "($") || strings.Contains(claim, "`)") {
		return true
	}
	if strings.Count(claim, "{")+strings.Count(claim, "}") >= 2 {
		return true
	}
	if strings.Count(claim, ";") >= 2 {
		return true
	}
	return false
}

func redact(input string) string {
	input = emailRE.ReplaceAllString(input, "[REDACTED_EMAIL]")
	input = phoneRE.ReplaceAllString(input, "${1}[REDACTED_PHONE]${3}")
	input = longIDRE.ReplaceAllString(input, "${1}[REDACTED_ID]${3}")
	input = credentialNameRE.ReplaceAllString(input, "[REDACTED_SECRET_NAME]")
	input = secretContextRE.ReplaceAllStringFunc(input, func(match string) string {
		if credentialNameRE.MatchString(match) || strings.Contains(strings.ToLower(match), "password") || strings.Contains(strings.ToLower(match), "secret") || strings.Contains(strings.ToLower(match), "token") || strings.Contains(strings.ToLower(match), "credential") {
			return "[REDACTED_SECRET_CONTEXT]"
		}
		return match
	})
	return input
}

func redactObservationValue(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	redacted := map[string]any{}
	for key, item := range value {
		redacted[key] = redactObservationAny(item)
	}
	return redacted
}

func redactObservationAny(value any) any {
	switch typed := value.(type) {
	case string:
		return redact(typed)
	case map[string]any:
		return redactObservationValue(typed)
	case []any:
		redacted := make([]any, 0, len(typed))
		for _, item := range typed {
			redacted = append(redacted, redactObservationAny(item))
		}
		return redacted
	default:
		return value
	}
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataFloat(metadata map[string]any, key string) float64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func lineageMetadata(sourceConfigID, ingestionJobID string) map[string]any {
	metadata := map[string]any{}
	if strings.TrimSpace(sourceConfigID) != "" {
		metadata["source_config_id"] = strings.TrimSpace(sourceConfigID)
	}
	if strings.TrimSpace(ingestionJobID) != "" {
		metadata["ingestion_job_id"] = strings.TrimSpace(ingestionJobID)
	}
	return metadata
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}
