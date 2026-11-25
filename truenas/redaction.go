package truenas

import (
	"regexp"
	"strings"
)

var (
	logBearerPattern      = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/-]+=*`)
	logAssignmentPattern  = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|private[_-]?key|credential|authorization)(?:\s*\[[^]]+\])?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	logGitHubTokenPattern = regexp.MustCompile(`(?i)\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]+\b`)
)

func redactAPIValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		redacted := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			if apiSensitiveKey(key) {
				redacted[key] = "[REDACTED]"
			} else {
				redacted[key] = redactAPIValue(child)
			}
		}
		return redacted
	case []interface{}:
		redacted := make([]interface{}, len(typed))
		for index, child := range typed {
			redacted[index] = redactAPIValue(child)
		}
		return redacted
	default:
		return value
	}
}

func apiSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, fragment := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "credential", "compose_yaml", "contents", "content"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

// SanitizeLogText removes common credential forms from untrusted text such
// as container logs. The configured API key is handled as an exact value in
// addition to pattern-based scrubbing and is never returned to callers.
func (c *Client) SanitizeLogText(text string) string {
	if c != nil && c.apiKey != "" {
		text = strings.ReplaceAll(text, c.apiKey, "[REDACTED]")
	}
	text = logBearerPattern.ReplaceAllString(text, `${1}[REDACTED]`)
	text = logAssignmentPattern.ReplaceAllString(text, `${1}[REDACTED]`)
	return logGitHubTokenPattern.ReplaceAllString(text, "[REDACTED]")
}
