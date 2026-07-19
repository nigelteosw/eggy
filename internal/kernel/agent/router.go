package agent

import "strings"

type Router struct {
	Repositories     []string
	ComplexityLength int
}

func (r Router) CodingIntent(input string) (string, bool) {
	lower := strings.ToLower(input)
	codingWords := []string{"fix ", "implement ", "code ", "failing test", "debug ", "refactor ", "repository", "repo "}
	isCoding := false
	for _, word := range codingWords {
		if strings.Contains(lower, word) {
			isCoding = true
			break
		}
	}
	if !isCoding {
		return "", false
	}
	for _, repository := range r.Repositories {
		if strings.Contains(lower, strings.ToLower(repository)) {
			return repository, true
		}
	}
	if len(r.Repositories) == 1 {
		return r.Repositories[0], true
	}
	return "", true
}

func (r Router) ComplexNonCoding(input string) bool {
	if _, coding := r.CodingIntent(input); coding {
		return false
	}
	limit := r.ComplexityLength
	if limit <= 0 {
		limit = 600
	}
	lower := strings.ToLower(input)
	markers := 0
	for _, word := range []string{"analyze", "tradeoff", "compare", "strategy", "multi-step", "implications"} {
		if strings.Contains(lower, word) {
			markers++
		}
	}
	return len(input) >= limit || markers >= 2
}
