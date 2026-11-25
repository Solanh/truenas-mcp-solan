package tools

import "testing"

func TestParseVMCreateConfigDefaults(t *testing.T) {
	config, err := parseVMCreateConfig(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != defaultVMName || config.VCPUs != 5 || config.MemoryMB != 16384 || config.DiskGiB != 256 {
		t.Fatalf("unexpected defaults: %+v", config)
	}
	if config.DiskName != "Databases/vms/codexvm-disk" {
		t.Fatalf("unexpected default disk name: %q", config.DiskName)
	}
}

func TestParseVMCreateConfigRejectsUnsafePaths(t *testing.T) {
	if _, err := parseVMCreateConfig(map[string]interface{}{"iso_path": "/mnt/Databases/vms/../secret.iso"}); err == nil {
		t.Fatal("expected unsafe ISO path to be rejected")
	}
	if _, err := parseVMCreateConfig(map[string]interface{}{"disk_name": "Databases/vms/../other"}); err == nil {
		t.Fatal("expected unsafe disk path to be rejected")
	}
}

func TestVMHelpers(t *testing.T) {
	if got := vmSlug("Codex VM"); got != "codex-vm" {
		t.Fatalf("vmSlug() = %q", got)
	}
	if got := vmDiskBytes(256); got != 274877906944 {
		t.Fatalf("vmDiskBytes() = %d", got)
	}
}
