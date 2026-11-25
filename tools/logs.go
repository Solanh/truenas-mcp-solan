package tools

import (
	"fmt"
	"strings"

	"github.com/truenas/truenas-mcp/truenas"
)

const appLifecycleLogPath = "/var/log/app_lifecycle.log"

func handleGetAppLifecycleLog(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName := ""
	if value, ok := args["app_name"].(string); ok {
		appName = strings.TrimSpace(value)
	}

	tailLines := 200
	if value, ok := args["tail_lines"].(float64); ok {
		tailLines = int(value)
	}
	if tailLines < 1 || tailLines > 2000 {
		return "", fmt.Errorf("tail_lines must be between 1 and 2000")
	}

	contents, err := client.DownloadFile("filesystem.get", []interface{}{appLifecycleLogPath}, "app_lifecycle.log")
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", appLifecycleLogPath, err)
	}

	lines := splitLogLines(string(contents))
	if appName != "" {
		filtered := make([]string, 0, len(lines))
		needle := strings.ToLower(appName)
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), needle) {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
	}

	truncated := len(lines) > tailLines
	if truncated {
		lines = lines[len(lines)-tailLines:]
	}

	return marshalJSON(map[string]interface{}{
		"path":          appLifecycleLogPath,
		"app_name":      appName,
		"line_count":    len(lines),
		"truncated":     truncated,
		"required_role": "FULL_ADMIN",
		"lines":         lines,
	})
}

func splitLogLines(contents string) []string {
	contents = strings.ReplaceAll(contents, "\r\n", "\n")
	contents = strings.TrimRight(contents, "\n")
	if contents == "" {
		return []string{}
	}
	return strings.Split(contents, "\n")
}
