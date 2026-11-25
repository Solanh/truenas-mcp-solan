package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
	"gopkg.in/yaml.v3"
)

const (
	customComposeConfigKey = "custom_compose_config_string"
	customAppCreateTTL     = time.Hour
	customAppUpdateTTL     = 30 * time.Minute
	customAppRedeployTTL   = 30 * time.Minute
)

var customAppNamePattern = regexp.MustCompile("^[a-z]([-a-z0-9]*[a-z0-9])?$")

type customAppAnalysis struct {
	Services     []string `json:"services"`
	Images       []string `json:"images,omitempty"`
	HostPorts    []int    `json:"host_ports,omitempty"`
	BindMounts   []string `json:"bind_mounts,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	ServiceCount int      `json:"service_count"`
	ComposeKeys  []string `json:"compose_keys,omitempty"`
}

type customAppPreflightResult struct {
	AppExists        bool        `json:"app_exists"`
	ExistingApp      interface{} `json:"existing_app,omitempty"`
	UsedPorts        []int       `json:"used_ports,omitempty"`
	ConflictingPorts []int       `json:"conflicting_ports,omitempty"`
	Warnings         []string    `json:"warnings,omitempty"`
}

func registerCustomAppTools(r *Registry) {
	r.tools["validate_custom_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "validate_custom_app",
			Description: "Validate a TrueNAS custom-app Docker Compose YAML document locally and run read-only preflight checks for duplicate app names and host-port conflicts.",
			InputSchema: customAppSchema(map[string]interface{}{
				"app_name":     map[string]interface{}{"type": "string", "description": "TrueNAS custom app instance name"},
				"compose_yaml": map[string]interface{}{"type": "string", "description": "Docker Compose YAML pasted into the TrueNAS custom-app form"},
			}),
		},
		Handler: handleValidateCustomApp,
	}

	r.tools["create_custom_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "create_custom_app",
			Description: "Create a TrueNAS custom application from Docker Compose YAML using custom_app=true and custom_compose_config_string. Returns a task_id and supports dry_run previews.",
			InputSchema: customAppSchema(map[string]interface{}{
				"app_name":     map[string]interface{}{"type": "string", "description": "TrueNAS custom app instance name"},
				"compose_yaml": map[string]interface{}{"type": "string", "description": "Docker Compose YAML pasted into the TrueNAS custom-app form"},
				"dry_run":      map[string]interface{}{"type": "boolean", "default": false},
			}),
		},
		Handler: r.handleCreateCustomApp,
	}

	r.tools["update_custom_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "update_custom_app",
			Description: "Replace a TrueNAS custom application's Docker Compose YAML. Job-based and supports dry_run previews.",
			InputSchema: customAppSchema(map[string]interface{}{
				"app_name":     map[string]interface{}{"type": "string", "description": "Existing TrueNAS custom app instance name"},
				"compose_yaml": map[string]interface{}{"type": "string", "description": "Replacement Docker Compose YAML"},
				"dry_run":      map[string]interface{}{"type": "boolean", "default": false},
			}),
		},
		Handler: r.handleUpdateCustomApp,
	}

	r.tools["redeploy_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "redeploy_app",
			Description: "Redeploy a TrueNAS application by stopping it, pulling current images, and restarting it. Supports dry_run and returns a task_id.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{"type": "string"},
					"dry_run":  map[string]interface{}{"type": "boolean", "default": false},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: r.handleRedeployApp,
	}

	r.tools["get_custom_app_diagnostics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_custom_app_diagnostics",
			Description: "Collect bounded diagnostics for a TrueNAS custom app: redacted config, live containers, ports, mounts, and recent lifecycle-log lines.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name":   map[string]interface{}{"type": "string"},
					"tail_lines": map[string]interface{}{"type": "integer", "default": 100, "minimum": 1, "maximum": 500},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: handleGetCustomAppDiagnostics,
	}
}

func customAppSchema(properties map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": properties, "required": []string{"app_name", "compose_yaml"}}
}

func validateCustomAppInput(args map[string]interface{}) (string, string, customAppAnalysis, error) {
	appName, ok := args["app_name"].(string)
	if !ok || strings.TrimSpace(appName) == "" {
		return "", "", customAppAnalysis{}, fmt.Errorf("app_name is required")
	}
	appName = strings.TrimSpace(appName)
	if len(appName) > 40 || !customAppNamePattern.MatchString(appName) {
		return "", "", customAppAnalysis{}, fmt.Errorf("invalid app_name: use 1-40 lowercase letters, numbers, or hyphens; start with a letter and do not end with a hyphen")
	}
	composeYAML, ok := args["compose_yaml"].(string)
	if !ok || strings.TrimSpace(composeYAML) == "" {
		return "", "", customAppAnalysis{}, fmt.Errorf("compose_yaml is required and must be non-empty")
	}
	analysis, err := analyzeCustomCompose(composeYAML)
	if err != nil {
		return "", "", customAppAnalysis{}, err
	}
	return appName, composeYAML, analysis, nil
}

func analyzeCustomCompose(composeYAML string) (customAppAnalysis, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(composeYAML), &document); err != nil {
		return customAppAnalysis{}, fmt.Errorf("invalid compose_yaml: %w", err)
	}
	root := &document
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return customAppAnalysis{}, fmt.Errorf("compose_yaml is empty")
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return customAppAnalysis{}, fmt.Errorf("compose_yaml must contain a top-level YAML mapping")
	}
	analysis := customAppAnalysis{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		analysis.ComposeKeys = append(analysis.ComposeKeys, key)
		if key != "services" {
			continue
		}
		if value.Kind != yaml.MappingNode || len(value.Content) == 0 {
			return customAppAnalysis{}, fmt.Errorf("compose_yaml.services must be a non-empty mapping")
		}
		for j := 0; j+1 < len(value.Content); j += 2 {
			analysis.Services = append(analysis.Services, value.Content[j].Value)
			analysis.ServiceCount++
			analyzeCustomService(value.Content[j+1], &analysis)
		}
	}
	if analysis.ServiceCount == 0 {
		return customAppAnalysis{}, fmt.Errorf("compose_yaml must define a non-empty services mapping")
	}
	analysis.Services = uniqueStrings(sortedStrings(analysis.Services))
	analysis.Images = uniqueStrings(sortedStrings(analysis.Images))
	analysis.BindMounts = uniqueStrings(sortedStrings(analysis.BindMounts))
	sort.Ints(analysis.HostPorts)
	analysis.HostPorts = uniqueInts(analysis.HostPorts)
	analysis.Warnings = uniqueStrings(analysis.Warnings)
	return analysis, nil
}

func analyzeCustomService(service *yaml.Node, analysis *customAppAnalysis) {
	if service.Kind != yaml.MappingNode {
		analysis.Warnings = append(analysis.Warnings, "a service definition is not a mapping")
		return
	}
	for i := 0; i+1 < len(service.Content); i += 2 {
		key, value := service.Content[i].Value, service.Content[i+1]
		switch key {
		case "image":
			if value.Value != "" {
				analysis.Images = append(analysis.Images, value.Value)
			}
		case "ports":
			collectCustomHostPorts(value, analysis)
		case "volumes":
			collectCustomBindMounts(value, analysis)
		case "privileged":
			if strings.EqualFold(value.Value, "true") {
				analysis.Warnings = append(analysis.Warnings, "one or more services enable privileged mode")
			}
		case "network_mode":
			if value.Value == "host" {
				analysis.Warnings = append(analysis.Warnings, "one or more services use host networking")
			}
		case "pid", "ipc":
			if value.Value == "host" {
				analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("one or more services share the host %s namespace", key))
			}
		case "environment", "env_file", "secrets":
			analysis.Warnings = append(analysis.Warnings, fmt.Sprintf("review service %s entries for embedded or externally supplied secrets", key))
		case "userns_mode":
			analysis.Warnings = append(analysis.Warnings, "one or more services customize user namespace handling")
		}
	}
}

func collectCustomHostPorts(node *yaml.Node, analysis *customAppAnalysis) {
	if node.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range node.Content {
		if item.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(item.Content); i += 2 {
				if item.Content[i].Value == "published" {
					if port, err := strconv.Atoi(item.Content[i+1].Value); err == nil {
						analysis.HostPorts = append(analysis.HostPorts, port)
					}
				}
			}
			continue
		}
		parts := strings.Split(strings.TrimSpace(item.Value), ":")
		if len(parts) >= 2 {
			if port, err := strconv.Atoi(strings.Split(parts[len(parts)-2], "-")[0]); err == nil {
				analysis.HostPorts = append(analysis.HostPorts, port)
			}
		}
	}
}

func collectCustomBindMounts(node *yaml.Node, analysis *customAppAnalysis) {
	if node.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode {
			parts := strings.Split(item.Value, ":")
			if len(parts) >= 2 && strings.HasPrefix(parts[0], "/") {
				analysis.BindMounts = append(analysis.BindMounts, parts[0])
			}
		} else if item.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(item.Content); i += 2 {
				if item.Content[i].Value == "source" && strings.HasPrefix(item.Content[i+1].Value, "/") {
					analysis.BindMounts = append(analysis.BindMounts, item.Content[i+1].Value)
				}
			}
		}
	}
}

func sortedStrings(values []string) []string {
	sort.Strings(values)
	return values
}

func uniqueStrings(values []string) []string {
	result, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueInts(values []int) []int {
	result, seen := []int{}, map[int]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func handleValidateCustomApp(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, composeYAML, analysis, err := validateCustomAppInput(args)
	if err != nil {
		return "", err
	}
	preflight, err := runCustomAppPreflight(client, appName, analysis.HostPorts)
	if err != nil {
		return "", err
	}
	analysis.Warnings = uniqueStrings(append(analysis.Warnings, preflight.Warnings...))
	return marshalJSON(map[string]interface{}{
		"valid": true, "app_name": appName, "yaml_bytes": len(composeYAML),
		"analysis": analysis, "preflight": preflight,
		"next_action": "Call create_custom_app with the same app_name and compose_yaml after reviewing warnings.",
	})
}

func runCustomAppPreflight(client *truenas.Client, appName string, hostPorts []int) (customAppPreflightResult, error) {
	preflight := customAppPreflightResult{}
	result, err := client.Call("app.query", []interface{}{[]interface{}{"name", "=", appName}}, map[string]interface{}{"limit": 1})
	if err != nil {
		return preflight, fmt.Errorf("failed to query existing app name: %w", err)
	}
	var apps []interface{}
	if err := json.Unmarshal(result, &apps); err != nil {
		return preflight, fmt.Errorf("failed to parse existing app query: %w", err)
	}
	preflight.AppExists = len(apps) > 0
	if preflight.AppExists {
		preflight.ExistingApp = redactSensitive(apps[0])
		preflight.Warnings = append(preflight.Warnings, fmt.Sprintf("app %q already exists; create_custom_app will fail", appName))
	}
	portsResult, err := client.Call("app.used_ports")
	if err != nil {
		preflight.Warnings = append(preflight.Warnings, fmt.Sprintf("could not check used application ports: %v", err))
		return preflight, nil
	}
	if err := json.Unmarshal(portsResult, &preflight.UsedPorts); err != nil {
		preflight.Warnings = append(preflight.Warnings, "TrueNAS returned an unrecognized used-port response")
		return preflight, nil
	}
	used := map[int]bool{}
	for _, port := range preflight.UsedPorts {
		used[port] = true
	}
	for _, port := range hostPorts {
		if used[port] {
			preflight.ConflictingPorts = append(preflight.ConflictingPorts, port)
		}
	}
	preflight.ConflictingPorts = uniqueInts(preflight.ConflictingPorts)
	if len(preflight.ConflictingPorts) > 0 {
		preflight.Warnings = append(preflight.Warnings, fmt.Sprintf("host ports already used by applications: %v", preflight.ConflictingPorts))
	}
	return preflight, nil
}

func (r *Registry) handleCreateCustomApp(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, composeYAML, analysis, err := validateCustomAppInput(args)
	if err != nil {
		return "", err
	}
	preflight, err := runCustomAppPreflight(client, appName, analysis.HostPorts)
	if err != nil {
		return "", err
	}
	analysis.Warnings = uniqueStrings(append(analysis.Warnings, preflight.Warnings...))
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		return marshalJSON(customAppDryRun("create_custom_app", appName, composeYAML, analysis, preflight, false))
	}
	result, err := client.Call("app.create", map[string]interface{}{
		"app_name": appName, "custom_app": true, customComposeConfigKey: composeYAML,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create custom app: %w", err)
	}
	jobID, err := parseAppJobID(result, "app.create")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("create_custom_app", args, jobID, customAppCreateTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{
		"app_name": appName, "custom_app": true, "task_id": task.TaskID,
		"task_status": task.Status, "poll_interval": task.PollInterval, "job_id": jobID,
		"analysis": analysis, "preflight": preflight,
		"message": fmt.Sprintf("Custom app creation started. Track progress with tasks_get using task_id: %s", task.TaskID),
	})
}

func (r *Registry) handleUpdateCustomApp(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, composeYAML, analysis, err := validateCustomAppInput(args)
	if err != nil {
		return "", err
	}
	current, err := queryAppByName(client, appName)
	if err != nil {
		return "", err
	}
	if custom, ok := current["custom_app"].(bool); !ok || !custom {
		return "", fmt.Errorf("app %q is not a custom app", appName)
	}
	preflight, err := runCustomAppPreflight(client, appName, analysis.HostPorts)
	if err != nil {
		return "", err
	}
	analysis.Warnings = uniqueStrings(append(analysis.Warnings, preflight.Warnings...))
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		currentConfig, _ := client.Call("app.config", appName)
		var config interface{}
		_ = json.Unmarshal(currentConfig, &config)
		result := customAppDryRun("update_custom_app", appName, composeYAML, analysis, preflight, true)
		result["current_config"] = redactSensitive(config)
		return marshalJSON(result)
	}
	result, err := client.Call("app.update", appName, map[string]interface{}{customComposeConfigKey: composeYAML})
	if err != nil {
		return "", fmt.Errorf("failed to update custom app: %w", err)
	}
	jobID, err := parseAppJobID(result, "app.update")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("update_custom_app", args, jobID, customAppUpdateTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{
		"app_name": appName, "task_id": task.TaskID, "task_status": task.Status,
		"poll_interval": task.PollInterval, "job_id": jobID, "analysis": analysis, "preflight": preflight,
		"message": fmt.Sprintf("Custom app update started. Track progress with tasks_get using task_id: %s", task.TaskID),
	})
}

func customAppDryRun(tool, appName, composeYAML string, analysis customAppAnalysis, preflight customAppPreflightResult, update bool) map[string]interface{} {
	op, target := "create", "app.create"
	if update {
		op, target = "update", "app.update"
	}
	return map[string]interface{}{
		"tool": tool, "app_name": appName, "yaml_bytes": len(composeYAML), "analysis": analysis, "preflight": preflight,
		"planned_actions": []map[string]interface{}{{"step": 1, "operation": op, "target": target, "details": map[string]interface{}{"custom_app": true, customComposeConfigKey: "<provided YAML>"}}},
		"warnings":        analysis.Warnings,
	}
}

func (r *Registry) handleRedeployApp(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || strings.TrimSpace(appName) == "" {
		return "", fmt.Errorf("app_name is required")
	}
	appName = strings.TrimSpace(appName)
	current, err := queryAppByName(client, appName)
	if err != nil {
		return "", err
	}
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		return marshalJSON(map[string]interface{}{
			"tool": "redeploy_app", "app_name": appName, "current_state": redactSensitive(current),
			"planned_actions": []map[string]interface{}{{"step": 1, "operation": "redeploy", "target": "app.redeploy", "details": map[string]interface{}{"app_name": appName}}},
			"warnings":        []string{"The app will be stopped, images will be pulled, and containers will be restarted."},
		})
	}
	result, err := client.Call("app.redeploy", appName)
	if err != nil {
		return "", fmt.Errorf("failed to redeploy app: %w", err)
	}
	jobID, err := parseAppJobID(result, "app.redeploy")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("redeploy_app", args, jobID, customAppRedeployTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{
		"app_name": appName, "task_id": task.TaskID, "task_status": task.Status,
		"poll_interval": task.PollInterval, "job_id": jobID,
		"message": fmt.Sprintf("App redeploy started. Track progress with tasks_get using task_id: %s", task.TaskID),
	})
}

func handleGetCustomAppDiagnostics(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || strings.TrimSpace(appName) == "" {
		return "", fmt.Errorf("app_name is required")
	}
	tailLines := 100
	if value, ok := args["tail_lines"].(float64); ok {
		tailLines = int(value)
	}
	if tailLines < 1 || tailLines > 500 {
		return "", fmt.Errorf("tail_lines must be between 1 and 500")
	}
	appName = strings.TrimSpace(appName)
	app, err := queryAppByName(client, appName)
	if err != nil {
		return "", err
	}
	configResult, err := client.Call("app.config", appName)
	if err != nil {
		return "", fmt.Errorf("failed to get app config: %w", err)
	}
	var config interface{}
	if err := json.Unmarshal(configResult, &config); err != nil {
		return "", fmt.Errorf("failed to parse app config: %w", err)
	}
	containersResult, err := client.Call("app.container_ids", appName, map[string]interface{}{"alive_only": false})
	if err != nil {
		return "", fmt.Errorf("failed to get app containers: %w", err)
	}
	var containers interface{}
	if err := json.Unmarshal(containersResult, &containers); err != nil {
		return "", fmt.Errorf("failed to parse app containers: %w", err)
	}
	portsResult, err := client.Call("app.used_ports")
	if err != nil {
		return "", fmt.Errorf("failed to get used app ports: %w", err)
	}
	var usedPorts interface{}
	if err := json.Unmarshal(portsResult, &usedPorts); err != nil {
		return "", fmt.Errorf("failed to parse used app ports: %w", err)
	}
	response := map[string]interface{}{
		"app_name": appName, "app": redactSensitive(app), "config": redactSensitive(config),
		"containers": redactSensitive(containers), "used_ports": redactSensitive(usedPorts),
	}
	if contents, logErr := client.DownloadFile("filesystem.get", []interface{}{appLifecycleLogPath}, "app_lifecycle.log"); logErr == nil {
		lines := splitLogLines(string(contents))
		needle := strings.ToLower(appName)
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), needle) {
				filtered = append(filtered, line)
			}
		}
		if len(filtered) > tailLines {
			filtered = filtered[len(filtered)-tailLines:]
		}
		response["lifecycle_log"] = map[string]interface{}{"path": appLifecycleLogPath, "lines": filtered, "tail_lines": tailLines}
	} else {
		response["lifecycle_log_error"] = logErr.Error()
	}
	return marshalJSON(response)
}

func queryAppByName(client *truenas.Client, appName string) (map[string]interface{}, error) {
	result, err := client.Call("app.query", []interface{}{[]interface{}{"name", "=", appName}}, map[string]interface{}{"limit": 1})
	if err != nil {
		return nil, fmt.Errorf("failed to query app %q: %w", appName, err)
	}
	var apps []map[string]interface{}
	if err := json.Unmarshal(result, &apps); err != nil {
		return nil, fmt.Errorf("failed to parse app query: %w", err)
	}
	if len(apps) == 0 {
		return nil, fmt.Errorf("app not found: %s", appName)
	}
	return apps[0], nil
}

func parseAppJobID(result json.RawMessage, method string) (int, error) {
	var jobID int
	if err := json.Unmarshal(result, &jobID); err == nil && jobID > 0 {
		return jobID, nil
	}
	var jobIDs []int
	if err := json.Unmarshal(result, &jobIDs); err == nil && len(jobIDs) > 0 && jobIDs[0] > 0 {
		return jobIDs[0], nil
	}
	return 0, fmt.Errorf("%s returned an unrecognized or empty job ID", method)
}

func redactSensitive(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				result[key] = "[REDACTED]"
			} else {
				result[key] = redactSensitive(item)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(typed))
		for i, item := range typed {
			result[i] = redactSensitive(item)
		}
		return result
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, part := range []string{"password", "passwd", "secret", "token", "api_key", "apikey", "private_key", "credential"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func boolArg(args map[string]interface{}, key string) bool {
	value, _ := args[key].(bool)
	return value
}
