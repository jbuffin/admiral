package judge

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

var supportedKeywords = map[string]bool{"type": true, "properties": true, "required": true, "additionalProperties": true,
	"enum": true, "maxLength": true, "items": true, "description": true, "title": true, "$schema": true}

var supportedTypes = map[string]bool{"object": true, "string": true, "number": true, "integer": true, "boolean": true, "array": true}

// checkSupported refuses a schema that uses anything validate does not check, so an
// unchecked constraint can never pass a reply as valid.
func checkSupported(schema map[string]any, root bool) error {
	if schema == nil {
		return fmt.Errorf("the call's schema is empty")
	}
	if root && schema["type"] != "object" {
		return fmt.Errorf("the call's schema root is not an object schema")
	}
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !supportedKeywords[k] {
			return fmt.Errorf("the call's schema uses %s, which judge calls don't support", k)
		}
	}
	if t, has := schema["type"]; has {
		name, isStr := t.(string)
		if !isStr || !supportedTypes[name] {
			return fmt.Errorf("the call's schema uses a type that judge calls don't support")
		}
	}
	if v, has := schema["enum"]; has {
		if _, ok := v.([]any); !ok {
			return fmt.Errorf("the call's schema uses an enum that is not an array, which judge calls don't support")
		}
	}
	if v, has := schema["required"]; has {
		list, ok := v.([]any)
		for _, e := range list {
			if _, isStr := e.(string); !isStr {
				ok = false
			}
		}
		if !ok {
			return fmt.Errorf("the call's schema uses a required that is not a list of names, which judge calls don't support")
		}
	}
	if v, has := schema["maxLength"]; has {
		if n, ok := v.(float64); !ok || n < 0 {
			return fmt.Errorf("the call's schema uses a maxLength that is not a non-negative number, which judge calls don't support")
		}
	}
	if props, has := schema["properties"]; has {
		m, ok := props.(map[string]any)
		if !ok {
			return fmt.Errorf("the call's schema uses properties that is not an object, which judge calls don't support")
		}
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			sub, ok := m[k].(map[string]any)
			if !ok {
				return fmt.Errorf("the call's schema uses a property schema that is not an object, which judge calls don't support")
			}
			if err := checkSupported(sub, false); err != nil {
				return err
			}
		}
	}
	if items, has := schema["items"]; has {
		sub, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("the call's schema uses items that is not one schema, which judge calls don't support")
		}
		if err := checkSupported(sub, false); err != nil {
			return err
		}
	}
	if ap, has := schema["additionalProperties"]; has && ap != false {
		return fmt.Errorf("the call's schema uses additionalProperties other than false, which judge calls don't support")
	}
	return nil
}

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
