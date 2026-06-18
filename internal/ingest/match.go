package ingest

import (
	"path"
	"regexp"
	"strings"
)

// MatchPath returns true when a relative path is allowed by include/exclude
// globs. Excludes win over includes. Empty includes mean "include everything".
func MatchPath(relPath string, include, exclude []string) bool {
	p := normalizePath(relPath)
	if p == "" {
		return false
	}
	for _, pattern := range exclude {
		if MatchPattern(pattern, p) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, pattern := range include {
		if MatchPattern(pattern, p) {
			return true
		}
	}
	return false
}

// MatchPattern matches slash-normalized paths with *, ?, ** and directory
// suffix patterns. Patterns without a slash match any path segment.
func MatchPattern(pattern, relPath string) bool {
	rawPattern := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if strings.HasSuffix(rawPattern, "/") {
		dirPattern := normalizePath(strings.TrimSuffix(rawPattern, "/"))
		p := normalizePath(relPath)
		return p == dirPattern || strings.HasPrefix(p, dirPattern+"/")
	}

	pattern = normalizePattern(pattern)
	relPath = normalizePath(relPath)
	if pattern == "" || relPath == "" {
		return false
	}

	if !strings.Contains(pattern, "/") {
		for _, segment := range strings.Split(relPath, "/") {
			if matchGlob(pattern, segment) {
				return true
			}
		}
		return false
	}
	return matchGlob(pattern, relPath)
}

func normalizePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

func normalizePattern(pattern string) string {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, "/")
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	return pattern
}

func matchGlob(pattern, value string) bool {
	re, err := regexp.Compile("^" + globToRegexp(pattern) + "$")
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func globToRegexp(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
					continue
				}
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	return b.String()
}
