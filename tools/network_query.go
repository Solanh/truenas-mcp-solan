package tools

import (
	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

func registerNetworkQueryTools(r *Registry) {
	r.tools["query_interfaces"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_interfaces",
			Description: "List TrueNAS network interfaces and configured IP aliases without changing network configuration.",
			InputSchema: objectSchema(map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "default": 100}}),
		},
		Handler: handleQueryInterfaces,
	}
	r.tools["query_static_routes"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_static_routes",
			Description: "List configured TrueNAS static routes without changing network configuration.",
			InputSchema: objectSchema(map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "default": 100}}),
		},
		Handler: handleQueryStaticRoutes,
	}
}

func handleQueryInterfaces(client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.Call("interface.query", []interface{}{}, queryOptions(args))
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func handleQueryStaticRoutes(client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.Call("staticroute.query", []interface{}{}, queryOptions(args))
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}
