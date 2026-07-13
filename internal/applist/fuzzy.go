package applist

import (
	"sort"
	"strings"
)

// Match ranks apps against query, offline and case-insensitive:
// exact name > prefix > word prefix > substring > all-words > subsequence.
// At most limit results.
func Match(apps []App, query string, limit int) []App {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || limit <= 0 {
		return nil
	}
	type scored struct {
		app   App
		score int
	}
	var ranked []scored
	for _, app := range apps {
		if s := score(strings.ToLower(app.Name), q); s > 0 {
			ranked = append(ranked, scored{app, s})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if len(ranked[i].app.Name) != len(ranked[j].app.Name) {
			return len(ranked[i].app.Name) < len(ranked[j].app.Name)
		}
		return ranked[i].app.Name < ranked[j].app.Name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]App, len(ranked))
	for i, r := range ranked {
		out[i] = r.app
	}
	return out
}

func score(name, q string) int {
	switch {
	case name == q:
		return 100
	case strings.HasPrefix(name, q):
		return 80
	case wordPrefix(name, q):
		return 60
	case strings.Contains(name, q):
		return 40
	case allWordsContained(name, q):
		return 30
	case subsequence(name, q):
		return 10
	default:
		return 0
	}
}

func wordPrefix(name, q string) bool {
	for _, w := range strings.Fields(name) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

func allWordsContained(name, q string) bool {
	words := strings.Fields(q)
	if len(words) < 2 {
		return false
	}
	for _, w := range words {
		if !strings.Contains(name, w) {
			return false
		}
	}
	return true
}

func subsequence(name, q string) bool {
	qr := []rune(strings.ReplaceAll(q, " ", ""))
	i := 0
	for _, r := range name {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}
