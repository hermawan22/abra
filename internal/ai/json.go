package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

type JSONRepairer interface {
	RepairJSON(ctx context.Context, raw string, schema JSONSchema) (string, error)
}

type JSONRepairerFunc func(ctx context.Context, raw string, schema JSONSchema) (string, error)

func (f JSONRepairerFunc) RepairJSON(ctx context.Context, raw string, schema JSONSchema) (string, error) {
	return f(ctx, raw, schema)
}

type DeterministicJSONRepairer struct{}

func DefaultJSONRepairer() JSONRepairer {
	return DeterministicJSONRepairer{}
}

func (DeterministicJSONRepairer) RepairJSON(_ context.Context, raw string, _ JSONSchema) (string, error) {
	repaired := strings.TrimSpace(raw)
	repaired = stripMarkdownFence(repaired)
	repaired = extractBalancedJSON(repaired)
	repaired = stripTrailingCommas(repaired)
	repaired = strings.TrimSpace(repaired)
	if repaired == strings.TrimSpace(raw) {
		return "", ErrRepairUnavailable
	}
	return repaired, nil
}

func ParseAndValidateJSON(ctx context.Context, raw string, schema JSONSchema, repairer JSONRepairer) (any, bool, []string, error) {
	value, parseErr := parseJSON(raw)
	if parseErr == nil {
		validationErrors, validationErr := ValidateJSONValue(value, schema)
		if validationErr == nil {
			return value, false, validationErrors, nil
		}
		if repairer == nil {
			return nil, false, validationErrors, validationErr
		}
	}

	if repairer == nil {
		if parseErr != nil {
			return nil, false, nil, fmt.Errorf("%w: parse json: %v", ErrInvalidResponse, parseErr)
		}
		return nil, false, nil, ErrValidationFailed
	}

	repairedRaw, repairErr := repairer.RepairJSON(ctx, raw, schema)
	if repairErr != nil {
		if parseErr != nil {
			return nil, false, nil, fmt.Errorf("%w: parse json: %v; repair: %v", ErrInvalidResponse, parseErr, repairErr)
		}
		return nil, false, nil, repairErr
	}
	value, err := parseJSON(repairedRaw)
	if err != nil {
		return nil, true, nil, fmt.Errorf("%w: parse repaired json: %v", ErrInvalidResponse, err)
	}
	validationErrors, validationErr := ValidateJSONValue(value, schema)
	if validationErr != nil {
		return nil, true, validationErrors, validationErr
	}
	return value, true, validationErrors, nil
}

func ValidateJSONValue(value any, schema JSONSchema) ([]string, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	errors := validateAgainstSchema("$", value, map[string]any(schema))
	if len(errors) > 0 {
		return errors, fmt.Errorf("%w: %s", ErrValidationFailed, strings.Join(errors, "; "))
	}
	return nil, nil
}

func parseJSON(raw string) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("multiple top-level json values")
	}
	return normalizeJSONNumbers(value), nil
}

func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeJSONNumbers(child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = normalizeJSONNumbers(child)
		}
		return typed
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return float64(integer)
		}
		if number, err := typed.Float64(); err == nil {
			return number
		}
		return typed.String()
	default:
		return value
	}
}

func validateAgainstSchema(path string, value any, schema map[string]any) []string {
	var errors []string
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		found := false
		for _, enumValue := range enumValues {
			if reflect.DeepEqual(normalizeJSONNumbers(enumValue), value) {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, fmt.Sprintf("%s must be one of %v", path, enumValues))
		}
	}

	types := schemaTypes(schema["type"])
	if len(types) == 0 {
		if _, ok := schema["properties"]; ok {
			types = []string{"object"}
		} else if _, ok := schema["items"]; ok {
			types = []string{"array"}
		}
	}
	if len(types) > 0 && !matchesAnyType(value, types) {
		errors = append(errors, fmt.Sprintf("%s must be %s", path, strings.Join(types, " or ")))
		return errors
	}

	for _, schemaType := range types {
		switch schemaType {
		case "object":
			object, ok := value.(map[string]any)
			if ok {
				errors = append(errors, validateObject(path, object, schema)...)
			}
		case "array":
			array, ok := value.([]any)
			if ok {
				errors = append(errors, validateArray(path, array, schema)...)
			}
		case "string":
			if text, ok := value.(string); ok {
				errors = append(errors, validateString(path, text, schema)...)
			}
		}
	}
	return errors
}

func validateObject(path string, object map[string]any, schema map[string]any) []string {
	var errors []string
	for _, required := range stringSlice(schema["required"]) {
		if _, ok := object[required]; !ok {
			errors = append(errors, fmt.Sprintf("%s.%s is required", path, required))
		}
	}

	properties := schemaMap(schema["properties"])
	for name, propertySchema := range properties {
		value, ok := object[name]
		if !ok {
			continue
		}
		childSchema, ok := propertySchema.(map[string]any)
		if !ok {
			continue
		}
		errors = append(errors, validateAgainstSchema(path+"."+name, value, childSchema)...)
	}
	if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
		for name := range object {
			if _, ok := properties[name]; !ok {
				errors = append(errors, fmt.Sprintf("%s.%s is not allowed", path, name))
			}
		}
	}
	return errors
}

func validateArray(path string, array []any, schema map[string]any) []string {
	var errors []string
	if min, ok := number(schema["minItems"]); ok && len(array) < int(min) {
		errors = append(errors, fmt.Sprintf("%s must contain at least %d items", path, int(min)))
	}
	if max, ok := number(schema["maxItems"]); ok && len(array) > int(max) {
		errors = append(errors, fmt.Sprintf("%s must contain at most %d items", path, int(max)))
	}
	itemsSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return errors
	}
	for index, item := range array {
		errors = append(errors, validateAgainstSchema(fmt.Sprintf("%s[%d]", path, index), item, itemsSchema)...)
	}
	return errors
}

func validateString(path string, text string, schema map[string]any) []string {
	var errors []string
	if min, ok := number(schema["minLength"]); ok && len(text) < int(min) {
		errors = append(errors, fmt.Sprintf("%s must have length at least %d", path, int(min)))
	}
	if max, ok := number(schema["maxLength"]); ok && len(text) > int(max) {
		errors = append(errors, fmt.Sprintf("%s must have length at most %d", path, int(max)))
	}
	if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err == nil && !compiled.MatchString(text) {
			errors = append(errors, fmt.Sprintf("%s must match pattern %q", path, pattern))
		}
	}
	return errors
}

func schemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		types := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				types = append(types, text)
			}
		}
		return types
	default:
		return nil
	}
}

func matchesAnyType(value any, types []string) bool {
	for _, schemaType := range types {
		if matchesType(value, schemaType) {
			return true
		}
	}
	return false
}

func matchesType(value any, schemaType string) bool {
	switch schemaType {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	output := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			output = append(output, text)
		}
	}
	return output
}

func schemaMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case JSONSchema:
		return map[string]any(typed)
	default:
		return nil
	}
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func stripMarkdownFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}
	if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	return trimmed
}

func extractBalancedJSON(raw string) string {
	start := strings.IndexAny(raw, "{[")
	if start < 0 {
		return raw
	}
	var stack []rune
	inString := false
	escaped := false
	for index, r := range raw[start:] {
		absolute := start + index
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{', '[':
			stack = append(stack, r)
		case '}', ']':
			if len(stack) == 0 {
				return raw
			}
			expected := '{'
			if r == ']' {
				expected = '['
			}
			if stack[len(stack)-1] != expected {
				return raw
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return raw[start : absolute+1]
			}
		}
	}
	return raw[start:]
}

func stripTrailingCommas(raw string) string {
	var builder strings.Builder
	inString := false
	escaped := false
	for index, r := range raw {
		if escaped {
			builder.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inString {
			builder.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			builder.WriteRune(r)
			inString = !inString
			continue
		}
		if !inString && r == ',' {
			rest := strings.TrimLeft(raw[index+len(string(r)):], " \n\r\t")
			if strings.HasPrefix(rest, "}") || strings.HasPrefix(rest, "]") {
				continue
			}
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
