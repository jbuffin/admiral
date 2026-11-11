package judge

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

// validate checks v against the subset of JSON Schema the judge calls use: type,
// properties, required, additionalProperties false, enum, maxLength and items.
func validate(schema map[string]any, v any, path string) error {
	if t, ok := schema["type"].(string); ok {
		if err := checkType(t, v, path); err != nil {
			return err
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if jsonEqual(e, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s is not one of the allowed values", path)
		}
	}
	if max, ok := schema["maxLength"].(float64); ok {
		if s, isStr := v.(string); isStr && float64(utf8.RuneCountInString(s)) > max {
			return fmt.Errorf("%s is longer than %v characters", path, max)
		}
	}
	switch val := v.(type) {
	case map[string]any:
		return validateObject(schema, val, path)
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range val {
				if err := validate(items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateObject(schema map[string]any, obj map[string]any, path string) error {
	props, _ := schema["properties"].(map[string]any)
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, there := obj[name]; !there {
					return fmt.Errorf("%s.%s is missing", path, name)
				}
			}
		}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sub, known := props[k].(map[string]any)
		if !known {
			if schema["additionalProperties"] == false {
				return fmt.Errorf("%s.%s is not allowed", path, k)
			}
			continue
		}
		if err := validate(sub, obj[k], path+"."+k); err != nil {
			return err
		}
	}
	return nil
}

func checkType(t string, v any, path string) error {
	ok := false
	switch t {
	case "object":
		_, ok = v.(map[string]any)
	case "array":
		_, ok = v.([]any)
	case "string":
		_, ok = v.(string)
	case "boolean":
		_, ok = v.(bool)
	case "number":
		_, ok = v.(float64)
	case "integer":
		f, isNum := v.(float64)
		ok = isNum && f == math.Trunc(f)
	default:
		ok = true
	}
	if !ok {
		return fmt.Errorf("%s is not %s", path, t)
	}
	return nil
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
