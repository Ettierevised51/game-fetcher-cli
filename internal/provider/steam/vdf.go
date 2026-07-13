package steam

import "strings"

// parseVDF parses the text KeyValues that `steamcmd +app_info_print` emits,
// leniently: anything that is not a quoted string or a brace (steamcmd
// banners, ANSI codes, progress lines) is skipped by the tokenizer, so the
// surrounding console noise does not matter.
func parseVDF(s string) map[string]any {
	toks := vdfTokens(s)
	root := map[string]any{}
	i := 0
	vdfPairs(root, toks, &i)
	return root
}

const (
	tokOpen  = "{"
	tokClose = "}"
)

// vdfTokens emits "{", "}" and quoted strings (prefixed with a quote so a
// literal value "{" cannot be confused with a brace token).
func vdfTokens(s string) []string {
	var toks []string
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			toks = append(toks, tokOpen)
		case '}':
			toks = append(toks, tokClose)
		case '"':
			var b strings.Builder
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					b.WriteByte(s[j+1])
					j += 2
					continue
				}
				b.WriteByte(s[j])
				j++
			}
			toks = append(toks, `"`+b.String())
			i = j
		}
	}
	return toks
}

func vdfPairs(into map[string]any, toks []string, i *int) {
	for *i < len(toks) {
		t := toks[*i]
		switch t {
		case tokClose:
			*i++
			return
		case tokOpen:
			*i++ // stray brace — skip
		default:
			key := t[1:]
			*i++
			if *i >= len(toks) {
				return
			}
			switch v := toks[*i]; v {
			case tokOpen:
				*i++
				child := map[string]any{}
				vdfPairs(child, toks, i)
				into[key] = child
			case tokClose:
				*i++ // dangling key — treat block as closed
				return
			default:
				into[key] = v[1:]
				*i++
			}
		}
	}
}

// vdfMap and vdfStr are nil-safe navigation helpers.
func vdfMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func vdfStr(v any) string {
	s, _ := v.(string)
	return s
}
