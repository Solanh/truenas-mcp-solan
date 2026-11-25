package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

const (
	defaultContainerLogLines = 100
	maxContainerLogLines     = 500
	defaultContainerLogTail  = 5 * time.Second
	defaultContainerLogWait  = 30 * time.Second
	maxContainerLogWait      = 60 * time.Second
	containerLogQuietPeriod  = 750 * time.Millisecond
)

type containerLogTarget struct {
	AppName     string `json:"app_name"`
	ServiceName string `json:"service_name,omitempty"`
	ContainerID string `json:"container_id"`
}

type containerLogResult struct {
	AppName        string    `json:"app_name"`
	ServiceName    string    `json:"service_name,omitempty"`
	ContainerID    string    `json:"container_id"`
	Lines          []string  `json:"lines"`
	LineCount      int       `json:"line_count"`
	Truncated      bool      `json:"truncated"`
	Matched        bool      `json:"matched"`
	TimedOut       bool      `json:"timed_out"`
	Pattern        string    `json:"pattern,omitempty"`
	MatchedLine    string    `json:"matched_line,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	CollectionNote string    `json:"collection_note,omitempty"`
}

func registerContainerLogTools(r *Registry) {
	r.tools["get_app_container_logs"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_app_container_logs",
			Description: "Return a bounded, sanitized tail of a TrueNAS app container's stdout/stderr logs. Read-only.",
			InputSchema: containerLogSchema(map[string]interface{}{
				"tail_lines": map[string]interface{}{"type": "integer", "default": defaultContainerLogLines, "minimum": 1, "maximum": maxContainerLogLines},
			}),
		},
		Handler: handleGetAppContainerLogs,
	}

	waitSchema := containerLogSchema(map[string]interface{}{
		"pattern":         map[string]interface{}{"type": "string", "description": "Literal substring to find in the log stream"},
		"tail_lines":      map[string]interface{}{"type": "integer", "default": defaultContainerLogLines, "minimum": 1, "maximum": maxContainerLogLines},
		"timeout_seconds": map[string]interface{}{"type": "integer", "default": int(defaultContainerLogWait / time.Second), "minimum": 1, "maximum": int(maxContainerLogWait / time.Second)},
		"case_sensitive":  map[string]interface{}{"type": "boolean", "default": true},
	})
	waitSchema["required"] = []string{"app_name", "pattern"}
	r.tools["wait_for_app_container_log"] = Tool{
		Definition: mcp.Tool{
			Name:        "wait_for_app_container_log",
			Description: "Wait for a literal text marker in a TrueNAS app container's sanitized logs, with a bounded timeout. Read-only.",
			InputSchema: waitSchema,
		},
		Handler: handleWaitForAppContainerLog,
	}
}

func containerLogSchema(extra map[string]interface{}) map[string]interface{} {
	properties := map[string]interface{}{
		"app_name":     map[string]interface{}{"type": "string", "description": "TrueNAS application name"},
		"service_name": map[string]interface{}{"type": "string", "description": "Optional Compose service name"},
		"container_id": map[string]interface{}{"type": "string", "description": "Optional exact container ID"},
	}
	for key, value := range extra {
		properties[key] = value
	}
	return map[string]interface{}{"type": "object", "properties": properties, "required": []string{"app_name"}}
}

func handleGetAppContainerLogs(client *truenas.Client, args map[string]interface{}) (string, error) {
	target, err := resolveContainerLogTarget(client, args)
	if err != nil {
		return "", err
	}
	tailLines, err := containerLogLinesArg(args)
	if err != nil {
		return "", err
	}

	result, err := collectContainerLogs(client, target, tailLines, defaultContainerLogTail, "", true)
	if err != nil {
		return "", err
	}
	return marshalJSON(result)
}

func handleWaitForAppContainerLog(client *truenas.Client, args map[string]interface{}) (string, error) {
	target, err := resolveContainerLogTarget(client, args)
	if err != nil {
		return "", err
	}
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	tailLines, err := containerLogLinesArg(args)
	if err != nil {
		return "", err
	}
	waitSeconds := int(defaultContainerLogWait / time.Second)
	if value, supplied := args["timeout_seconds"]; supplied {
		parsed, valid := integerValue(value)
		if !valid || parsed < 1 || parsed > int(maxContainerLogWait/time.Second) {
			return "", fmt.Errorf("timeout_seconds must be between 1 and %d", int(maxContainerLogWait/time.Second))
		}
		waitSeconds = parsed
	}
	caseSensitive := true
	if value, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = value
	}

	result, err := collectContainerLogs(client, target, tailLines, time.Duration(waitSeconds)*time.Second, pattern, caseSensitive)
	if err != nil {
		return "", err
	}
	return marshalJSON(result)
}

func resolveContainerLogTarget(client *truenas.Client, args map[string]interface{}) (containerLogTarget, error) {
	appName, ok := args["app_name"].(string)
	if !ok || strings.TrimSpace(appName) == "" {
		return containerLogTarget{}, fmt.Errorf("app_name is required")
	}
	appName = strings.TrimSpace(appName)

	raw, err := client.Call("app.container_ids", appName, map[string]interface{}{"alive_only": false})
	if err != nil {
		return containerLogTarget{}, fmt.Errorf("failed to query containers for %q: %w", appName, err)
	}
	var containers map[string]interface{}
	if err := json.Unmarshal(raw, &containers); err != nil {
		return containerLogTarget{}, fmt.Errorf("failed to parse containers for %q: %w", appName, err)
	}
	if len(containers) == 0 {
		return containerLogTarget{}, fmt.Errorf("no containers found for app %q", appName)
	}

	wantedID := optionalString(args, "container_id")
	wantedService := optionalString(args, "service_name")
	type candidate struct{ id, service string }
	candidates := make([]candidate, 0, len(containers))
	for id, value := range containers {
		service := ""
		if details, ok := value.(map[string]interface{}); ok {
			for _, key := range []string{"service_name", "service"} {
				if name, ok := details[key].(string); ok && name != "" {
					service = name
					break
				}
			}
		}
		if wantedID != "" && id != wantedID {
			continue
		}
		if wantedService != "" && service != wantedService {
			continue
		}
		candidates = append(candidates, candidate{id: id, service: service})
	}
	if len(candidates) == 0 {
		return containerLogTarget{}, fmt.Errorf("no container matched app %q, service %q, and container ID %q", appName, wantedService, wantedID)
	}
	if len(candidates) > 1 {
		services := make([]string, 0, len(candidates))
		for _, item := range candidates {
			if item.service == "" {
				services = append(services, item.id)
			} else {
				services = append(services, item.service)
			}
		}
		sort.Strings(services)
		return containerLogTarget{}, fmt.Errorf("app %q has multiple matching containers; specify service_name or container_id: %s", appName, strings.Join(services, ", "))
	}
	return containerLogTarget{AppName: appName, ServiceName: candidates[0].service, ContainerID: candidates[0].id}, nil
}

func collectContainerLogs(client *truenas.Client, target containerLogTarget, maxLines int, timeout time.Duration, pattern string, caseSensitive bool) (containerLogResult, error) {
	started := time.Now().UTC()
	result := containerLogResult{
		AppName:     target.AppName,
		ServiceName: target.ServiceName,
		ContainerID: target.ContainerID,
		Lines:       make([]string, 0, maxLines),
		Pattern:     client.SanitizeLogText(pattern),
		StartedAt:   started,
	}

	params, err := json.Marshal(map[string]interface{}{
		"app_name":     target.AppName,
		"container_id": target.ContainerID,
		"tail_lines":   maxLines,
	})
	if err != nil {
		return result, fmt.Errorf("failed to encode container log filter: %w", err)
	}
	subscription, err := client.Subscribe("app.container_log_follow:" + string(params))
	if err != nil {
		return result, fmt.Errorf("failed to subscribe to container logs: %w", err)
	}
	defer subscription.Close()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var quiet <-chan time.Time
	var quietTimer *time.Timer
	defer func() {
		if quietTimer != nil {
			quietTimer.Stop()
		}
	}()

	for {
		select {
		case event, ok := <-subscription.Events():
			if !ok {
				if subscription.Err() != nil {
					return result, fmt.Errorf("container log subscription ended: %w", subscription.Err())
				}
				result.CollectionNote = "subscription closed"
				return finishContainerLogResult(result), nil
			}
			if event.Error != nil {
				return result, fmt.Errorf("TrueNAS container log subscription returned an error")
			}
			line, ok := event.Fields["data"].(string)
			if !ok {
				continue
			}
			line = client.SanitizeLogText(strings.TrimRight(line, "\r\n"))
			if len(result.Lines) == maxLines {
				copy(result.Lines, result.Lines[1:])
				result.Lines[len(result.Lines)-1] = line
				result.Truncated = true
			} else {
				result.Lines = append(result.Lines, line)
			}
			if pattern != "" && containsLogPattern(line, pattern, caseSensitive) {
				result.Matched = true
				result.MatchedLine = line
				return finishContainerLogResult(result), nil
			}
			if quietTimer == nil {
				quietTimer = time.NewTimer(containerLogQuietPeriod)
			} else {
				if !quietTimer.Stop() {
					select {
					case <-quietTimer.C:
					default:
					}
				}
				quietTimer.Reset(containerLogQuietPeriod)
			}
			quiet = quietTimer.C
		case <-quiet:
			result.CollectionNote = "quiet period reached"
			return finishContainerLogResult(result), nil
		case <-deadline.C:
			if pattern != "" {
				result.TimedOut = !result.Matched
				result.CollectionNote = "timeout reached before pattern matched"
			} else {
				result.CollectionNote = "collection timeout reached"
			}
			return finishContainerLogResult(result), nil
		}
	}
}

func finishContainerLogResult(result containerLogResult) containerLogResult {
	result.LineCount = len(result.Lines)
	result.FinishedAt = time.Now().UTC()
	return result
}

func containsLogPattern(line, pattern string, caseSensitive bool) bool {
	if caseSensitive {
		return strings.Contains(line, pattern)
	}
	return strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
}

func containerLogLinesArg(args map[string]interface{}) (int, error) {
	lines := defaultContainerLogLines
	if value, supplied := args["tail_lines"]; supplied {
		parsed, valid := integerValue(value)
		if !valid || parsed < 1 || parsed > maxContainerLogLines {
			return 0, fmt.Errorf("tail_lines must be between 1 and %d", maxContainerLogLines)
		}
		lines = parsed
	}
	return lines, nil
}

func integerValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed >= 0
	case float64:
		return int(typed), typed >= 0 && typed == float64(int(typed))
	default:
		return 0, false
	}
}
