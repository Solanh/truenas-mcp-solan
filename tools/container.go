package tools

import (
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

func handleGetAppContainerStatus(client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	aliveOnly := false
	if value, ok := args["alive_only"].(bool); ok {
		aliveOnly = value
	}

	result, err := client.Call("app.container_ids", appName, map[string]interface{}{
		"alive_only": aliveOnly,
	})
	if err != nil {
		return "", fmt.Errorf("failed to query containers for %q: %w", appName, err)
	}

	var containers map[string]interface{}
	if err := json.Unmarshal(result, &containers); err != nil {
		return "", fmt.Errorf("failed to parse container status: %w", err)
	}

	return marshalJSON(map[string]interface{}{
		"app_name":   appName,
		"alive_only": aliveOnly,
		"containers": containers,
	})
}

func registerAppContainerStatus(r *Registry) {
	r.tools["get_app_container_status"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_app_container_status",
			Description: "Query the live runtime state of every container in a TrueNAS app, including short-lived helper containers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type": "string",
					},
					"alive_only": map[string]interface{}{
						"type":    "boolean",
						"default": false,
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: handleGetAppContainerStatus,
	}
}
