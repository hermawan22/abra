package memory

import "strings"

func compactList(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
