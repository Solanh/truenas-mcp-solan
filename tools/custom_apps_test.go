package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeCustomCompose(t *testing.T) {
	compose := `services:
  web:
    image: nginx:latest
    ports:
      - "8080:80"
    volumes:
      - /mnt/tank/apps/web:/usr/share/nginx/html
    privileged: true
    environment:
      MODE: production
`
	analysis, err := analyzeCustomCompose(compose)
	if err != nil {
		t.Fatalf("analyzeCustomCompose() error = %v", err)
	}
	if analysis.ServiceCount != 1 || len(analysis.Services) != 1 || analysis.Services[0] != "web" {
		t.Fatalf("unexpected services: %#v", analysis)
	}
	if len(analysis.HostPorts) != 1 || analysis.HostPorts[0] != 8080 {
		t.Fatalf("unexpected host ports: %#v", analysis.HostPorts)
	}
	if len(analysis.BindMounts) != 1 || analysis.BindMounts[0] != "/mnt/tank/apps/web" {
		t.Fatalf("unexpected bind mounts: %#v", analysis.BindMounts)
	}
	if !strings.Contains(strings.Join(analysis.Warnings, " "), "privileged") {
		t.Fatalf("expected privileged warning: %#v", analysis.Warnings)
	}
}

func TestAnalyzeCustomComposeRejectsInvalidDocuments(t *testing.T) {
	cases := []string{
		"not: [valid",
		"- list",
		"services: {}",
		"version: '3.9'",
	}
	for _, compose := range cases {
		if _, err := analyzeCustomCompose(compose); err == nil {
			t.Errorf("analyzeCustomCompose(%q) expected an error", compose)
		}
	}
}

func TestValidateCustomAppInput(t *testing.T) {
	args := map[string]interface{}{
		"app_name":     "my-custom-app",
		"compose_yaml": "services:\n  app:\n    image: example/app:latest\n",
	}
	name, yamlText, analysis, err := validateCustomAppInput(args)
	if err != nil {
		t.Fatalf("validateCustomAppInput() error = %v", err)
	}
	if name != "my-custom-app" || yamlText == "" || analysis.ServiceCount != 1 {
		t.Fatalf("unexpected result: %q %q %#v", name, yamlText, analysis)
	}
	for _, name := range []string{"BadName", "-bad", "bad-", "bad_name", strings.Repeat("a", 41)} {
		args["app_name"] = name
		if _, _, _, err := validateCustomAppInput(args); err == nil {
			t.Errorf("validateCustomAppInput(%q) expected an error", name)
		}
	}
}

func TestParseAppJobID(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  int
	}{
		{"scalar", "42", 42},
		{"array", "[42]", 42},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAppJobID(json.RawMessage(test.value), "app.create")
			if err != nil || got != test.want {
				t.Fatalf("parseAppJobID() = %d, %v; want %d", got, err, test.want)
			}
		})
	}
	if _, err := parseAppJobID(json.RawMessage("{}"), "app.create"); err == nil {
		t.Fatal("parseAppJobID() expected an error for an object")
	}
}

func TestRedactSensitive(t *testing.T) {
	value := map[string]interface{}{
		"password": "secret",
		"nested":   map[string]interface{}{"api_key": "key", "safe": "value"},
	}
	redacted := redactSensitive(value).(map[string]interface{})
	if redacted["password"] != "[REDACTED]" {
		t.Fatalf("password was not redacted: %#v", redacted)
	}
	nested := redacted["nested"].(map[string]interface{})
	if nested["api_key"] != "[REDACTED]" || nested["safe"] != "value" {
		t.Fatalf("nested redaction failed: %#v", redacted)
	}
}
