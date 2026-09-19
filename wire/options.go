package wire

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/coder/acp-go-sdk"

	"github.com/savid/acp-go-core/process"
)

// StringMapOption validates a string map and copies it at the request boundary.
func StringMapOption(value any, path string) (map[string]string, *acp.RequestError) {
	switch typed := value.(type) {
	case map[string]string:
		return maps.Clone(typed), nil
	case map[string]any:
		result := make(map[string]string, len(typed))
		for key, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, Unsupported(path + "." + key)
			}

			result[key] = text
		}

		return result, nil
	default:
		return nil, Unsupported(path)
	}
}

// StringSliceOption validates and copies an ordered string list.
func StringSliceOption(value any, path string) ([]string, *acp.RequestError) {
	switch typed := value.(type) {
	case []string:
		return slices.Clone(typed), nil
	case []any:
		result := make([]string, 0, len(typed))
		for index, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, Unsupported(fmt.Sprintf("%s[%d]", path, index))
			}

			result = append(result, text)
		}

		return result, nil
	default:
		return nil, Unsupported(path)
	}
}

// CloneMap copies JSON metadata, including typed environment maps.
func CloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}

	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = CloneValue(value)
	}

	return cloned
}

// CloneValue copies mutable JSON values and typed string maps and slices.
func CloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return CloneMap(typed)
	case map[string]string:
		return maps.Clone(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = CloneValue(item)
		}

		return cloned
	case []string:
		return slices.Clone(typed)
	default:
		return typed
	}
}

// MergeMap recursively overlays metadata without retaining caller-owned containers.
func MergeMap(base map[string]any, overlay map[string]any) map[string]any {
	result := CloneMap(base)
	if result == nil {
		result = map[string]any{}
	}

	for key, value := range overlay {
		if valueMap, ok := value.(map[string]any); ok {
			if existing, ok := result[key].(map[string]any); ok {
				result[key] = MergeMap(existing, valueMap)

				continue
			}
		}

		result[key] = CloneValue(value)
	}

	return result
}

// ValidateSessionEnvironment maps environment and PATH validation to owned fields.
func ValidateSessionEnvironment(env map[string]string, dirs []string, path string) *acp.RequestError {
	if err := process.ValidateNames(env); err != nil {
		var nameErr *process.NameError
		if errors.As(err, &nameErr) {
			return Unsupported(path + ".env." + nameErr.Key)
		}

		return Unsupported(path + ".env")
	}

	if err := process.ValidateExtraPathDirs(dirs); err != nil {
		var dirErr *process.PathDirError
		if errors.As(err, &dirErr) {
			return Unsupported(fmt.Sprintf("%s.extraPathDirs[%d]", path, dirErr.Index))
		}

		return Unsupported(path + ".extraPathDirs")
	}

	return nil
}

// StructuredOutputAdvertisement is the _meta.<vendor>.structuredOutput
// capability: where the schema is configured, where the parsed answer rides
// on the prompt response, and the schema dialect.
func StructuredOutputAdvertisement(vendor string) map[string]any {
	return map[string]any{
		"config": MetaOptionPath(vendor, "outputSchema"),
		"result": "_meta." + vendor + ".structuredOutput",
		"schema": "json_schema",
	}
}

// ValidateConcurrencyLimits refuses negative session and client-call bounds.
func ValidateConcurrencyLimits(maxActiveSessions, maxConcurrentClientCalls int) error {
	if maxActiveSessions < 0 || maxConcurrentClientCalls < 0 {
		return errors.New("concurrency limits must not be negative")
	}

	return nil
}
