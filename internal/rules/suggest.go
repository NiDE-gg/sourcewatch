package rules

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxSuggestions is Discord's limit on autocomplete choices.
const MaxSuggestions = 25

// Suggest returns candidates similar to query, best first, for player and map
// values with no exact match (C7). Order: exact, prefix, substring, then near
// misses by edit distance (a swapped pair of letters counts as one edit). Case
// is ignored. It returns nil when nothing is similar.
func Suggest(query string, candidates []string, limit int) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	if limit <= 0 || limit > MaxSuggestions {
		limit = MaxSuggestions
	}
	maxDist := utf8.RuneCountInString(q) / 4
	if maxDist < 1 {
		maxDist = 1
	}
	if maxDist > 3 {
		maxDist = 3
	}
	type scored struct {
		s          string
		tier, dist int
	}
	var out []scored
	seen := map[string]bool{}
	for _, c := range candidates {
		l := strings.ToLower(c)
		if c == "" || seen[l] {
			continue
		}
		seen[l] = true
		switch {
		case l == q:
			out = append(out, scored{c, 0, 0})
		case strings.HasPrefix(l, q):
			out = append(out, scored{c, 1, 0})
		case strings.Contains(l, q):
			out = append(out, scored{c, 2, 0})
		default:
			if d := distance([]rune(q), []rune(l)); d <= maxDist {
				out = append(out, scored{c, 3, d})
			} else if d := bestSubstringDistance(q, l); d <= maxDist {
				out = append(out, scored{c, 3, d})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		if len(a.s) != len(b.s) {
			return len(a.s) < len(b.s)
		}
		return a.s < b.s
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return nil
	}
	res := make([]string, len(out))
	for i, o := range out {
		res[i] = o.s
	}
	return res
}

// bestSubstringDistance compares q with every window of l the same length as q,
// so "de_dsut" finds "de_dust2" even though the whole string differs by more.
func bestSubstringDistance(q, l string) int {
	qr, lr := []rune(q), []rune(l)
	if len(lr) <= len(qr) {
		return len(qr) + len(lr)
	}
	best := len(qr)
	for i := 0; i+len(qr) <= len(lr); i++ {
		if d := distance(qr, lr[i:i+len(qr)]); d < best {
			best = d
		}
	}
	return best
}

// distance is the optimal string alignment distance (Levenshtein plus adjacent swaps).
func distance(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
