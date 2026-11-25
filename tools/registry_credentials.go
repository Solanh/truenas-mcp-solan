package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

const genericTaskTTL = 30 * time.Minute

func registerRegistryTools(r *Registry) {
	r.tools["query_app_registries"] = Tool{Definition: mcp.Tool{Name: "query_app_registries", Description: "List configured TrueNAS application registries with credentials redacted.", InputSchema: objectSchema(map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "uri": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer", "default": 50}})}, Handler: handleQueryAppRegistries}
	r.tools["create_app_registry"] = Tool{Definition: mcp.Tool{Name: "create_app_registry", Description: "Create an application registry credential. Password input is write-only and is never returned.", InputSchema: objectSchema(map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "uri": map[string]interface{}{"type": "string"}, "username": map[string]interface{}{"type": "string"}, "password": map[string]interface{}{"type": "string"}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: handleCreateAppRegistry}
	r.tools["update_app_registry"] = Tool{Definition: mcp.Tool{Name: "update_app_registry", Description: "Update an application registry credential. Password input is write-only and is never returned.", InputSchema: objectSchema(map[string]interface{}{"id": map[string]interface{}{"type": "integer"}, "name": map[string]interface{}{"type": "string"}, "uri": map[string]interface{}{"type": "string"}, "username": map[string]interface{}{"type": "string"}, "password": map[string]interface{}{"type": "string"}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: handleUpdateAppRegistry}
	r.tools["delete_app_registry"] = Tool{Definition: mcp.Tool{Name: "delete_app_registry", Description: "Delete an application registry credential. Use dry_run first for a preview.", InputSchema: objectSchema(map[string]interface{}{"id": map[string]interface{}{"type": "integer"}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: handleDeleteAppRegistry}
	r.tools["query_app_images"] = Tool{Definition: mcp.Tool{Name: "query_app_images", Description: "List cached TrueNAS application images.", InputSchema: objectSchema(map[string]interface{}{"image": map[string]interface{}{"type": "string"}, "limit": map[string]interface{}{"type": "integer", "default": 50}})}, Handler: handleQueryAppImages}
	r.tools["pull_app_image"] = Tool{Definition: mcp.Tool{Name: "pull_app_image", Description: "Pull an application image, optionally using a registry credential. Returns a task ID.", InputSchema: objectSchema(map[string]interface{}{"image": map[string]interface{}{"type": "string"}, "username": map[string]interface{}{"type": "string"}, "password": map[string]interface{}{"type": "string"}, "registry_uri": map[string]interface{}{"type": "string"}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: r.handlePullAppImage}
}

func objectSchema(properties map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": properties}
}

func handleQueryAppRegistries(client *truenas.Client, args map[string]interface{}) (string, error) {
	filters := []interface{}{}
	if name := optionalString(args, "name"); name != "" {
		filters = append(filters, []interface{}{"name", "=", name})
	}
	if uri := optionalString(args, "uri"); uri != "" {
		filters = append(filters, []interface{}{"uri", "=", uri})
	}
	result, err := client.Call("app.registry.query", filters, queryOptions(args))
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func handleCreateAppRegistry(client *truenas.Client, args map[string]interface{}) (string, error) {
	name, err := requiredString(args, "name")
	if err != nil {
		return "", err
	}
	username, err := requiredString(args, "username")
	if err != nil {
		return "", err
	}
	password, err := requiredString(args, "password")
	if err != nil {
		return "", err
	}
	uri := optionalString(args, "uri")
	if uri == "" {
		uri = "https://index.docker.io/v1/"
	}
	if err := validateRegistryURI(uri); err != nil {
		return "", err
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "create_app_registry", "dry_run": true, "name": name, "uri": uri, "username": username, "password_configured": password != ""})
	}
	result, err := client.Call("app.registry.create", map[string]interface{}{"name": name, "uri": uri, "username": username, "password": password})
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func handleUpdateAppRegistry(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := requiredNumber(args, "id")
	if err != nil {
		return "", err
	}
	update := map[string]interface{}{}
	for _, key := range []string{"name", "uri", "username", "password"} {
		if value, ok := args[key]; ok {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return "", fmt.Errorf("%s must be a non-empty string", key)
			}
			update[key] = text
		}
	}
	if len(update) == 0 {
		return "", fmt.Errorf("at least one of name, uri, username, or password is required")
	}
	if uri, ok := update["uri"].(string); ok {
		if err := validateRegistryURI(uri); err != nil {
			return "", err
		}
	}
	if boolArg(args, "dry_run") {
		preview := map[string]interface{}{"tool": "update_app_registry", "dry_run": true, "id": id}
		for key, value := range update {
			if key == "password" {
				preview["password_configured"] = value.(string) != ""
			} else {
				preview[key] = value
			}
		}
		return marshalJSON(preview)
	}
	result, err := client.Call("app.registry.update", int(id), update)
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func handleDeleteAppRegistry(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := requiredNumber(args, "id")
	if err != nil {
		return "", err
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "delete_app_registry", "dry_run": true, "id": id, "warning": "This removes the registry credential and cannot be undone."})
	}
	result, err := client.Call("app.registry.delete", int(id))
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func handleQueryAppImages(client *truenas.Client, args map[string]interface{}) (string, error) {
	filters := []interface{}{}
	if image := optionalString(args, "image"); image != "" {
		filters = append(filters, []interface{}{"repo_tags", "in", []interface{}{image}})
	}
	result, err := client.Call("app.image.query", filters, queryOptions(args))
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func (r *Registry) handlePullAppImage(client *truenas.Client, args map[string]interface{}) (string, error) {
	image, err := requiredString(args, "image")
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(image, " \t\r\n") {
		return "", fmt.Errorf("image must not contain whitespace")
	}
	username := optionalString(args, "username")
	password, passwordSet := args["password"]
	passwordText, passwordOK := passwordSet && password != nil, false
	if passwordSet {
		passwordTextValue, ok := password.(string)
		passwordOK = ok
		if ok {
			passwordText = passwordTextValue != ""
		}
	}
	if passwordSet && !passwordOK {
		return "", fmt.Errorf("password must be a string")
	}
	if passwordText && username == "" {
		return "", fmt.Errorf("username is required when password is supplied")
	}
	registryURI := optionalString(args, "registry_uri")
	if registryURI != "" {
		if err := validateRegistryURI(registryURI); err != nil {
			return "", err
		}
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "pull_app_image", "dry_run": true, "image": image, "username": username, "password_configured": passwordText, "registry_uri": registryURI})
	}
	params := map[string]interface{}{"image": image}
	if username != "" || passwordText || registryURI != "" {
		auth := map[string]interface{}{"username": username, "password": ""}
		if passwordSet {
			auth["password"] = password.(string)
		}
		if registryURI != "" {
			auth["registry_uri"] = registryURI
		}
		params["auth_config"] = auth
	}
	result, err := client.Call("app.image.pull", params)
	if err != nil {
		return "", err
	}
	jobID, err := parseAppJobID(result, "app.image.pull")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("pull_app_image", args, jobID, genericTaskTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"task_id": task.TaskID, "job_id": jobID, "image": image})
}

func queryOptions(args map[string]interface{}) map[string]interface{} {
	limit, err := requiredNumber(args, "limit")
	if err != nil || limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	return map[string]interface{}{"limit": int(limit)}
}

func requiredString(args map[string]interface{}, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return strings.TrimSpace(value), nil
}

func optionalString(args map[string]interface{}, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func requiredNumber(args map[string]interface{}, key string) (float64, error) {
	switch value := args[key].(type) {
	case float64:
		if value > 0 && value == float64(int(value)) {
			return value, nil
		}
	case int:
		if value > 0 {
			return float64(value), nil
		}
	}
	return 0, fmt.Errorf("%s must be a positive integer", key)
}

func validateRegistryURI(uri string) error {
	if !strings.HasPrefix(strings.ToLower(uri), "https://") {
		return fmt.Errorf("registry URI must use https://")
	}
	return nil
}

func safeJSON(raw json.RawMessage) (string, error) {
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("failed to parse TrueNAS response: %w", err)
	}
	return marshalJSON(sanitizeRegistryValue(value))
}

func sanitizeRegistryValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		clean := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			if strings.Contains(strings.ToLower(key), "password") {
				clean["password_configured"] = child != nil
				continue
			}
			clean[key] = sanitizeRegistryValue(child)
		}
		return clean
	case []interface{}:
		clean := make([]interface{}, len(typed))
		for index, child := range typed {
			clean[index] = sanitizeRegistryValue(child)
		}
		return clean
	default:
		return value
	}
}
