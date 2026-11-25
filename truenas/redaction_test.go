package truenas

import "testing"

func TestSanitizeLogTextRemovesCredentialForms(t *testing.T) {
	client, err := NewClient("truenas.local", "api-key-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := "Authorization: Bearer abc.def; password=super-secret token=ghp_example-secret api_key=api-key-value"
	output := client.SanitizeLogText(input)
	for _, secret := range []string{"abc.def", "super-secret", "ghp_example-secret", "api-key-value"} {
		if contains(output, secret) {
			t.Fatalf("sanitized log contains %q: %s", secret, output)
		}
	}
	if !contains(output, "[REDACTED]") {
		t.Fatalf("sanitized log did not contain redaction marker: %s", output)
	}
}

func TestSanitizeLogTextPreservesOrdinaryDiagnostics(t *testing.T) {
	client, err := NewClient("truenas.local", "api-key-value", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := "Listening for Jobs\nrunner version 2.329.0"
	if got := client.SanitizeLogText(input); got != input {
		t.Fatalf("ordinary diagnostics changed: %q", got)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
