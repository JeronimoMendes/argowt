// Package filter provides deterministic, UI-independent resource filtering.
package filter

import (
	"strings"
	"unicode"
)

// Match reports whether every whitespace-separated query token is a
// case-insensitive subsequence of at least one field.
func Match(query string, fields ...string) bool {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return true
	}

	normalized := make([]string, len(fields))
	for i, field := range fields {
		normalized[i] = strings.ToLower(field)
	}
	for _, token := range tokens {
		matched := false
		for _, field := range normalized {
			if subsequence(token, field) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func subsequence(needle, haystack string) bool {
	wanted := []rune(needle)
	if len(wanted) == 0 {
		return true
	}
	index := 0
	for _, candidate := range haystack {
		if unicode.ToLower(candidate) == unicode.ToLower(wanted[index]) {
			index++
			if index == len(wanted) {
				return true
			}
		}
	}
	return false
}
