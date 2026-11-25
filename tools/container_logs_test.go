package tools

import (
	"testing"

	"github.com/truenas/truenas-mcp/tasks"
	"github.com/truenas/truenas-mcp/truenas"
)

func TestContainerLogPatternMatching(t *testing.T) {
	if !containsLogPattern("Listening for Jobs", "Listening for Jobs", true) {
		t.Fatal("exact literal did not match")
	}
	if containsLogPattern("listening for jobs", "Listening for Jobs", true) {
		t.Fatal("case-sensitive match unexpectedly matched")
	}
	if !containsLogPattern("listening for jobs", "Listening for Jobs", false) {
		t.Fatal("case-insensitive literal did not match")
	}
}

func TestContainerLogArguments(t *testing.T) {
	if got, err := containerLogLinesArg(map[string]interface{}{}); err != nil || got != defaultContainerLogLines {
		t.Fatalf("default tail_lines = %d, %v", got, err)
	}
	for _, value := range []interface{}{0, 501, "100", 1.5} {
		if _, err := containerLogLinesArg(map[string]interface{}{"tail_lines": value}); err == nil {
			t.Fatalf("tail_lines %v was accepted", value)
		}
	}
	if got, err := containerLogLinesArg(map[string]interface{}{"tail_lines": float64(25)}); err != nil || got != 25 {
		t.Fatalf("valid tail_lines = %d, %v", got, err)
	}
}

func TestContainerLogSanitizesBeforeReturning(t *testing.T) {
	client, err := truenas.NewClient("truenas.local", "api-key-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := client.SanitizeLogText("Listening for Jobs token=ghp_example-secret")
	if got == "Listening for Jobs token=ghp_example-secret" || containsString(got, "ghp_example-secret") {
		t.Fatalf("secret was not sanitized: %s", got)
	}
}

func TestContainerLogToolsAreRegisteredWithRequiredFields(t *testing.T) {
	client, err := truenas.NewClient("truenas.local", "api-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(client, tasks.NewManager(client, tasks.PollerConfig{}))
	seen := map[string]bool{}
	for _, tool := range registry.ListTools() {
		if tool.Name == "get_app_container_logs" || tool.Name == "wait_for_app_container_log" {
			seen[tool.Name] = true
		}
		if tool.Name == "wait_for_app_container_log" {
			required, _ := tool.InputSchema["required"].([]string)
			if len(required) != 2 || required[0] != "app_name" || required[1] != "pattern" {
				t.Fatalf("unexpected wait tool required fields: %#v", tool.InputSchema["required"])
			}
		}
	}
	if !seen["get_app_container_logs"] || !seen["wait_for_app_container_log"] {
		t.Fatalf("container log tools were not registered: %#v", seen)
	}
}

func containsString(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
