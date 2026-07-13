package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// loadLayer reads one layer file together with its conf.d drop-ins and
// include files. Drop-in pickup applies only to the layer file itself;
// included files may in turn use `include`, but get no conf.d of their own.
func loadLayer(path string) (map[string]any, error) {
	return expandFile(path, true, map[string]bool{})
}

// expandFile loads path and merges its extra sources over its own values:
// first conf.d/*.yaml next to it (when withConfD is set, lexicographic
// order), then files from the `include` directive in listed order.
// active tracks the current include chain to detect cycles.
func expandFile(path string, withConfD bool, active map[string]bool) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if active[abs] {
		return nil, fmt.Errorf("%s: include cycle", path)
	}
	active[abs] = true
	defer delete(active, abs)

	out, err := readYAMLMap(path)
	if err != nil {
		return nil, err
	}
	includes, err := popIncludes(out, path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)

	if withConfD {
		drops, err := filepath.Glob(filepath.Join(dir, "conf.d", "*.yaml"))
		if err != nil {
			return nil, err
		}
		sort.Strings(drops)
		for _, drop := range drops {
			sub, err := expandFile(drop, false, active)
			if err != nil {
				return nil, err
			}
			out = mergeMaps(out, sub)
		}
	}

	for _, inc := range includes {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(dir, incPath)
		}
		sub, err := expandFile(incPath, false, active)
		if err != nil {
			return nil, fmt.Errorf("%s: include %q: %w", path, inc, err)
		}
		out = mergeMaps(out, sub)
	}
	return out, nil
}

func readYAMLMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// popIncludes extracts and removes the `include` directive: a single path
// or a list of paths, relative ones resolved against the file's directory.
func popIncludes(m map[string]any, path string) ([]string, error) {
	v, ok := m["include"]
	if !ok {
		return nil, nil
	}
	delete(m, "include")
	switch t := v.(type) {
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, entry := range t {
			s, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("%s: include entries must be strings, got %T", path, entry)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: include must be a string or a list of strings", path)
	}
}
