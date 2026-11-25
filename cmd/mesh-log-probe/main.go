// Command mesh-log-probe is an opt-in live smoke test for the production
// bounded container-log MCP tool. It retains the historical command name.
package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/truenas/truenas-mcp/tasks"
	"github.com/truenas/truenas-mcp/tools"
	"github.com/truenas/truenas-mcp/truenas"
)

func main() {
	endpoint := os.Getenv("TRUENAS_URL")
	apiKey := os.Getenv("TRUENAS_API_KEY")
	if endpoint == "" || apiKey == "" {
		log.Fatal("TRUENAS_URL and TRUENAS_API_KEY are required")
	}

	client, err := truenas.NewClient(endpoint, apiKey, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	if err := client.Authenticate(); err != nil {
		log.Fatal(err)
	}

	timeoutSeconds := 30
	if value := os.Getenv("CONTAINER_LOG_TIMEOUT_SECONDS"); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			log.Fatalf("invalid CONTAINER_LOG_TIMEOUT_SECONDS: %v", parseErr)
		}
		timeoutSeconds = parsed
	}
	args := map[string]interface{}{
		"app_name":        envOrDefault("CONTAINER_LOG_APP", "meshcentral"),
		"pattern":         envOrDefault("CONTAINER_LOG_PATTERN", "Listening for Jobs"),
		"timeout_seconds": timeoutSeconds,
	}
	if value := os.Getenv("CONTAINER_LOG_SERVICE"); value != "" {
		args["service_name"] = value
	}
	if value := os.Getenv("CONTAINER_LOG_CONTAINER_ID"); value != "" {
		args["container_id"] = value
	}

	manager := tasks.NewManager(client, tasks.PollerConfig{PollInterval: time.Second, CleanupInterval: time.Minute})
	result, err := tools.NewRegistry(client, manager).CallTool("wait_for_app_container_log", args)
	if err != nil {
		log.Fatal(err)
	}
	var formatted interface{}
	if err := json.Unmarshal([]byte(result), &formatted); err != nil {
		log.Fatal(fmt.Errorf("tool returned invalid JSON: %w", err))
	}
	encoded, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
