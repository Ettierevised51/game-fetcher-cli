package config

// mergeMaps returns a new map with src deep-merged over dst: nested mappings
// are merged recursively, everything else (scalars, lists) is replaced by
// the src value. Neither argument is modified.
func mergeMaps(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if dm, ok := out[k].(map[string]any); ok {
			if sm, ok := v.(map[string]any); ok {
				out[k] = mergeMaps(dm, sm)
				continue
			}
		}
		out[k] = v
	}
	return out
}
