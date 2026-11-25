package tasks

import (
	"reflect"
	"testing"
)

func TestRedactArgumentsDoesNotMutateInput(t *testing.T) {
	args := map[string]interface{}{
		"password":     "secret",
		"compose_yaml": "MESHWARD_API_TOKEN: secret",
		"nested":       map[string]interface{}{"token": "nested-secret", "safe": "ok"},
	}
	redacted := RedactArguments(args)
	if redacted["password"] != "[REDACTED]" || redacted["compose_yaml"] != "[REDACTED]" {
		t.Fatalf("top-level secrets were not redacted: %#v", redacted)
	}
	nested := redacted["nested"].(map[string]interface{})
	if nested["token"] != "[REDACTED]" || nested["safe"] != "ok" {
		t.Fatalf("nested secrets were not redacted: %#v", nested)
	}
	if !reflect.DeepEqual(args["password"], "secret") || args["compose_yaml"] != "MESHWARD_API_TOKEN: secret" {
		t.Fatal("redaction mutated the input")
	}
}
