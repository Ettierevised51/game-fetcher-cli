package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// resolveExtends flattens profile inheritance in the fully merged tree:
// for every profile with an `extends` key, the parent chain is resolved and
// merged under the child (child values win), and the key is removed. It runs
// after all layers and includes are merged, so a profile may extend one
// defined anywhere in the tree. A reference to a missing profile is a hard
// error naming that profile.
func resolveExtends(root map[string]any) error {
	rawProfiles, ok := root["profiles"]
	if !ok || rawProfiles == nil {
		return nil
	}
	profiles, ok := rawProfiles.(map[string]any)
	if !ok {
		return errors.New("profiles must be a mapping")
	}

	resolved := make(map[string]map[string]any, len(profiles))

	var resolve func(name string, chain []string) (map[string]any, error)
	resolve = func(name string, chain []string) (map[string]any, error) {
		if body, done := resolved[name]; done {
			return body, nil
		}
		if slices.Contains(chain, name) {
			return nil, fmt.Errorf("extends cycle: %s", strings.Join(append(chain, name), " -> "))
		}
		body, err := profileMap(profiles, name)
		if err != nil {
			return nil, err
		}
		rawParent, hasParent := body["extends"]
		if !hasParent {
			resolved[name] = body
			return body, nil
		}
		parentName, ok := rawParent.(string)
		if !ok {
			return nil, fmt.Errorf("profile %q: extends must be a profile name (string)", name)
		}
		if _, exists := profiles[parentName]; !exists {
			return nil, fmt.Errorf("profile %q extends unknown profile %q", name, parentName)
		}
		parent, err := resolve(parentName, append(chain, name))
		if err != nil {
			return nil, err
		}
		child := make(map[string]any, len(body))
		for k, v := range body {
			if k != "extends" {
				child[k] = v
			}
		}
		merged := mergeMaps(parent, child)
		resolved[name] = merged
		return merged, nil
	}

	// Sorted for deterministic error reporting.
	for _, name := range slices.Sorted(maps.Keys(profiles)) {
		if _, err := resolve(name, nil); err != nil {
			return err
		}
	}
	for name, body := range resolved {
		profiles[name] = body
	}
	return nil
}

func profileMap(profiles map[string]any, name string) (map[string]any, error) {
	switch t := profiles[name].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return t, nil
	default:
		return nil, fmt.Errorf("profile %q must be a mapping", name)
	}
}
