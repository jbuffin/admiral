package config

import "encoding/json"

// withNewFleetSources adds a GitHub source for each fleet in the written fleets that data lacks,
// so the source and its fleet land in one write. The template is the first GitHub source in data.
func withNewFleetSources(data []byte, set []keyValue) ([]keyValue, error) {
	var fleets json.RawMessage
	for _, kv := range set {
		switch kv.key {
		case "sources":
			return set, nil
		case "fleets":
			fleets = kv.value
		}
	}
	if fleets == nil {
		return set, nil
	}
	top, err := ParseObject(data)
	if err != nil {
		return nil, err
	}
	old := NewObject()
	if raw, ok := top.Get("fleets"); ok {
		if old, err = ParseObject(raw); err != nil {
			return nil, err
		}
	}
	next, err := ParseObject(fleets)
	if err != nil {
		return nil, err
	}
	sources := NewObject()
	if raw, ok := top.Get("sources"); ok {
		if sources, err = ParseObject(raw); err != nil {
			return nil, err
		}
	}
	var template *Object
	for _, key := range sources.Keys() {
		raw, _ := sources.Get(key)
		if src, err := ParseObject(raw); err == nil && stringOf(src, "plugin") == "github" {
			template = src
			break
		}
	}
	added := false
	for _, fleet := range next.Keys() {
		if _, existed := old.Get(fleet); existed {
			continue
		}
		sources.Set(freeGitHubKey(sources, fleet), githubSourceFor(template, fleet))
		added = true
	}
	if !added {
		return set, nil
	}
	return append(set[:len(set):len(set)], keyValue{"sources", sources.JSON()}), nil
}
