package tools

import (
	"strings"
	"testing"
)

func TestSanitizeRegistryValueRemovesPasswords(t *testing.T) {
	value := map[string]interface{}{"name": "private", "password": "do-not-return", "nested": map[string]interface{}{"password": "also-do-not-return"}}
	result := sanitizeRegistryValue(value).(map[string]interface{})
	if _, ok := result["password"]; ok {
		t.Fatal("password field was returned")
	}
	if result["password_configured"] != true {
		t.Fatal("password_configured was not set")
	}
	encoded, err := marshalJSON(result)
	if err != nil || strings.Contains(encoded, "do-not-return") || strings.Contains(encoded, "also-do-not-return") {
		t.Fatalf("sanitized output leaked password: %s", encoded)
	}
}

func TestSafeMountPathRejectsTraversal(t *testing.T) {
	if _, err := safeMountPath(map[string]interface{}{"path": "/mnt/Apps/../secret"}, "path"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := safeMountPath(map[string]interface{}{"path": "/tmp/secret"}, "path"); err == nil {
		t.Fatal("non-/mnt path was accepted")
	}
}

func TestAuthorizedEd25519KeyFormat(t *testing.T) {
	public := make([]byte, 32)
	line := authorizedEd25519Key(public, "meshward")
	if !strings.HasPrefix(line, "ssh-ed25519 ") || !strings.HasSuffix(line, " meshward\n") {
		t.Fatalf("unexpected authorized key format: %q", line)
	}
}
