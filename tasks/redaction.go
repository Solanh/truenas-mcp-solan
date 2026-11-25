package tasks

import "strings"

// RedactValue returns a recursively copied value suitable for task state and
// diagnostics. Sensitive fields are replaced before they enter the task store.
func RedactValue(value interface{}, key string) interface{} {
	if sensitiveTaskKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		redacted := make(map[string]interface{}, len(typed))
		for childKey, child := range typed {
			redacted[childKey] = RedactValue(child, childKey)
		}
		return redacted
	case []interface{}:
		redacted := make([]interface{}, len(typed))
		for index, child := range typed {
			redacted[index] = RedactValue(child, "")
		}
		return redacted
	default:
		return value
	}
}

func RedactArguments(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}
	return RedactValue(args, "").(map[string]interface{})
}

func sensitiveTaskKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, fragment := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "credential", "compose_yaml", "contents", "content"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
