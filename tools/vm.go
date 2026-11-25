package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/truenas"
)

const (
	defaultVMISOPath   = "/mnt/Databases/vms"
	defaultVMName      = "CodexVM"
	defaultVMVCPUs     = 5
	defaultVMMemoryMB  = 16384
	defaultVMDiskGiB   = 256
	defaultVMNICAttach = "br0"
)

var safeVMName = regexp.MustCompile(`^[[:alnum:]][[:alnum:]_-]{0,62}$`)
var safeSSHUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type vmCreateConfig struct {
	Name, Description, ISOPath, ISODirectory string
	VCPUs, MemoryMB                          int
	DiskGiB                                  int64
	DiskName, NICAttach                      string
	Autostart, Start                         bool
}

func parseVMCreateConfig(args map[string]interface{}) (vmCreateConfig, error) {
	config := vmCreateConfig{
		Name: defaultVMName, ISODirectory: defaultVMISOPath,
		VCPUs: defaultVMVCPUs, MemoryMB: defaultVMMemoryMB,
		DiskGiB: defaultVMDiskGiB, NICAttach: defaultVMNICAttach,
	}
	if value, ok := args["name"].(string); ok && value != "" {
		config.Name = value
	}
	if !safeVMName.MatchString(config.Name) {
		return config, fmt.Errorf("name must be 1-63 characters and contain only letters, numbers, underscores, or hyphens")
	}
	if value, ok := args["description"].(string); ok {
		config.Description = value
	}
	if value, ok := args["iso_path"].(string); ok {
		config.ISOPath = value
	}
	if value, ok := args["iso_directory"].(string); ok && value != "" {
		config.ISODirectory = value
	}
	if !strings.HasPrefix(config.ISODirectory, "/mnt/") || strings.Contains(config.ISODirectory, "..") {
		return config, fmt.Errorf("iso_directory must be an absolute /mnt path without '..'")
	}
	if value, ok := args["vcpus"].(float64); ok {
		config.VCPUs = int(value)
	}
	if config.VCPUs < 1 || config.VCPUs > 16 {
		return config, fmt.Errorf("vcpus must be between 1 and 16")
	}
	if value, ok := args["memory_mb"].(float64); ok {
		config.MemoryMB = int(value)
	}
	if config.MemoryMB < 20 {
		return config, fmt.Errorf("memory_mb must be at least 20")
	}
	if value, ok := args["disk_size_gib"].(float64); ok {
		config.DiskGiB = int64(value)
	}
	if config.DiskGiB < 1 || config.DiskGiB > 16384 {
		return config, fmt.Errorf("disk_size_gib must be between 1 and 16384")
	}
	if value, ok := args["disk_name"].(string); ok && value != "" {
		config.DiskName = value
	} else {
		config.DiskName = fmt.Sprintf("Databases/vms/%s-disk", vmSlug(config.Name))
	}
	if !strings.HasPrefix(config.DiskName, "Databases/vms/") || strings.Contains(config.DiskName, "..") {
		return config, fmt.Errorf("disk_name must be below Databases/vms and cannot contain '..'")
	}
	if value, ok := args["nic_attach"].(string); ok && value != "" {
		config.NICAttach = value
	}
	if strings.ContainsAny(config.NICAttach, " \t\r\n") {
		return config, fmt.Errorf("nic_attach cannot contain whitespace")
	}
	if value, ok := args["autostart"].(bool); ok {
		config.Autostart = value
	}
	if value, ok := args["start"].(bool); ok {
		config.Start = value
	}
	if config.ISOPath != "" && (!strings.HasPrefix(config.ISOPath, "/mnt/") || strings.Contains(config.ISOPath, "..") || !strings.HasSuffix(strings.ToLower(config.ISOPath), ".iso")) {
		return config, fmt.Errorf("iso_path must be an absolute /mnt path ending in .iso and cannot contain '..'")
	}
	return config, nil
}

func vmSlug(name string) string {
	value := strings.ToLower(name)
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, value)
	return strings.Trim(value, "-")
}

func vmDiskBytes(gib int64) int64 { return gib * 1024 * 1024 * 1024 }

func findUbuntuISO(client *truenas.Client, directory string) (string, error) {
	result, err := client.Call("filesystem.listdir", directory, []interface{}{}, map[string]interface{}{"select": []string{"name", "path", "type"}})
	if err != nil {
		return "", fmt.Errorf("failed to list ISO directory: %w", err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(result, &entries); err != nil {
		return "", fmt.Errorf("failed to parse ISO directory: %w", err)
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		path, _ := entry["path"].(string)
		entryType, _ := entry["type"].(string)
		lowerName := strings.ToLower(name)
		if entryType == "FILE" && strings.HasSuffix(lowerName, ".iso") && strings.Contains(lowerName, "ubuntu") && path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no Ubuntu ISO found in %s", directory)
	}
	sort.Strings(paths)
	return paths[0], nil
}

func vmIDFromResult(result json.RawMessage) (int, error) {
	var value map[string]interface{}
	if err := json.Unmarshal(result, &value); err != nil {
		return 0, fmt.Errorf("failed to parse VM response: %w", err)
	}
	id, ok := value["id"].(float64)
	if !ok || id < 1 || id != math.Trunc(id) {
		return 0, fmt.Errorf("VM response did not contain a valid id")
	}
	return int(id), nil
}

func vmAlreadyExists(client *truenas.Client, name string) (bool, error) {
	result, err := client.Call("vm.query")
	if err != nil {
		return false, fmt.Errorf("failed to query existing VMs: %w", err)
	}
	var vms []map[string]interface{}
	if err := json.Unmarshal(result, &vms); err != nil {
		return false, fmt.Errorf("failed to parse existing VMs: %w", err)
	}
	for _, vm := range vms {
		if vmName, _ := vm["name"].(string); vmName == name {
			return true, nil
		}
	}
	return false, nil
}

func handleFindVMISO(client *truenas.Client, args map[string]interface{}) (string, error) {
	directory := defaultVMISOPath
	if value, ok := args["directory"].(string); ok && value != "" {
		directory = value
	}
	path, err := findUbuntuISO(client, directory)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"directory": directory, "iso_path": path, "selection": "first Ubuntu .iso in lexicographic path order"})
}

func handleCreateVM(client *truenas.Client, args map[string]interface{}) (string, error) {
	config, err := parseVMCreateConfig(args)
	if err != nil {
		return "", err
	}
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		return marshalJSON(map[string]interface{}{
			"dry_run": true, "operation": "create_vm",
			"planned_actions": []map[string]interface{}{
				{"step": 1, "operation": "discover", "target": config.ISODirectory, "details": "Select the first Ubuntu .iso in lexicographic path order unless iso_path is supplied."},
				{"step": 2, "operation": "create", "target": config.Name, "details": map[string]interface{}{"vcpus": config.VCPUs, "memory_mb": config.MemoryMB, "autostart": config.Autostart}},
				{"step": 3, "operation": "create", "target": config.DiskName, "details": map[string]interface{}{"disk_size_bytes": vmDiskBytes(config.DiskGiB), "disk_size_gib": config.DiskGiB, "sparse": true}},
				{"step": 4, "operation": "attach", "target": "installation media and network", "details": map[string]interface{}{"iso_path": config.ISOPath, "nic_attach": config.NICAttach}},
			},
			"note": "No VM or disk has been created.",
		})
	}
	if config.ISOPath == "" {
		config.ISOPath, err = findUbuntuISO(client, config.ISODirectory)
		if err != nil {
			return "", err
		}
	}
	if exists, err := vmAlreadyExists(client, config.Name); err != nil {
		return "", err
	} else if exists {
		return "", fmt.Errorf("a VM named %q already exists", config.Name)
	}

	created, err := client.Call("vm.create", map[string]interface{}{
		"name": config.Name, "description": config.Description, "vcpus": config.VCPUs, "cores": 1, "threads": 1,
		"memory": config.MemoryMB, "bootloader": "UEFI", "autostart": config.Autostart, "ensure_display_device": true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create VM: %w", err)
	}
	vmID, err := vmIDFromResult(created)
	if err != nil {
		return "", err
	}
	diskCreated := false
	rollback := func(cause error) (string, error) {
		if _, deleteErr := client.Call("vm.delete", vmID); deleteErr != nil {
			return "", fmt.Errorf("%w; rollback vm.delete(%d) also failed: %v", cause, vmID, deleteErr)
		}
		if diskCreated {
			if _, deleteErr := client.Call("pool.dataset.delete", config.DiskName, map[string]interface{}{"recursive": false, "force": false}); deleteErr != nil {
				return "", fmt.Errorf("%w; VM %d was removed but rollback could not delete zvol %s: %v", cause, vmID, config.DiskName, deleteErr)
			}
		}
		return "", fmt.Errorf("%w; partially created VM %d and its disk were rolled back", cause, vmID)
	}
	if _, err = client.Call("vm.device.create", map[string]interface{}{"vm": vmID, "attributes": map[string]interface{}{"dtype": "DISK", "type": "VIRTIO", "create_zvol": true, "zvol_name": config.DiskName, "zvol_volsize": vmDiskBytes(config.DiskGiB)}}); err != nil {
		return rollback(fmt.Errorf("failed to create VM disk: %w", err))
	}
	diskCreated = true
	if _, err = client.Call("vm.device.create", map[string]interface{}{"vm": vmID, "attributes": map[string]interface{}{"dtype": "CDROM", "path": config.ISOPath}}); err != nil {
		return rollback(fmt.Errorf("failed to attach installation ISO: %w", err))
	}
	if _, err = client.Call("vm.device.create", map[string]interface{}{"vm": vmID, "attributes": map[string]interface{}{"dtype": "NIC", "type": "VIRTIO", "nic_attach": config.NICAttach}}); err != nil {
		return rollback(fmt.Errorf("failed to attach VM network: %w", err))
	}

	response := map[string]interface{}{
		"success": true, "vm_id": vmID, "name": config.Name, "iso_path": config.ISOPath,
		"disk_name": config.DiskName, "disk_size_gib": config.DiskGiB, "vcpus": config.VCPUs,
		"memory_mb": config.MemoryMB, "nic_attach": config.NICAttach, "autostart": config.Autostart,
		"next_steps": []string{"Start the VM, install Ubuntu through the console, enable OpenSSH, then run vm_ssh_test with the guest address and key path."},
	}
	if config.Start {
		if _, err := client.Call("vm.start", vmID, map[string]interface{}{"overcommit": false}); err != nil {
			return "", fmt.Errorf("VM %d was created but could not be started: %w", vmID, err)
		}
		response["started"] = true
	}
	return marshalJSON(response)
}

func handleStartVM(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := requiredVMID(args)
	if err != nil {
		return "", err
	}
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		return marshalJSON(map[string]interface{}{"dry_run": true, "operation": "vm.start", "vm_id": id, "note": "No VM was started."})
	}
	overcommit, _ := args["overcommit"].(bool)
	if _, err := client.Call("vm.start", id, map[string]interface{}{"overcommit": overcommit}); err != nil {
		return "", fmt.Errorf("failed to start VM %d: %w", id, err)
	}
	return marshalJSON(map[string]interface{}{"success": true, "vm_id": id, "started": true, "overcommit": overcommit})
}

func handleDeleteVMZvol(client *truenas.Client, args map[string]interface{}) (string, error) {
	name, _ := args["zvol_name"].(string)
	if !strings.HasPrefix(name, "Databases/vms/") || strings.Contains(name, "..") || strings.ContainsAny(name, " \t\r\n") {
		return "", fmt.Errorf("zvol_name must be an exact zvol path below Databases/vms")
	}
	result, err := client.Call("pool.dataset.query", []interface{}{[]interface{}{"id", "=", name}}, map[string]interface{}{})
	if err != nil {
		return "", fmt.Errorf("failed to verify zvol: %w", err)
	}
	var datasets []map[string]interface{}
	if err := json.Unmarshal(result, &datasets); err != nil {
		return "", fmt.Errorf("failed to parse zvol verification response: %w", err)
	}
	if len(datasets) != 1 || datasets[0]["id"] != name || datasets[0]["type"] != "VOLUME" {
		return "", fmt.Errorf("refusing to delete %q: exact VOLUME zvol was not found", name)
	}
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		return marshalJSON(map[string]interface{}{"dry_run": true, "operation": "pool.dataset.delete", "zvol_name": name, "note": "No zvol was deleted."})
	}
	if _, err := client.Call("pool.dataset.delete", name, map[string]interface{}{"recursive": false, "force": false}); err != nil {
		return "", fmt.Errorf("failed to delete zvol %s: %w", name, err)
	}
	return marshalJSON(map[string]interface{}{"success": true, "zvol_name": name, "deleted": true})
}

func handleGetVMConsole(client *truenas.Client, args map[string]interface{}) (string, error) {
	id, err := requiredVMID(args)
	if err != nil {
		return "", err
	}
	result, err := client.Call("vm.get_console", id)
	if err != nil {
		return "", fmt.Errorf("failed to get VM %d console: %w", id, err)
	}
	var console string
	if err := json.Unmarshal(result, &console); err != nil {
		return "", fmt.Errorf("failed to parse VM console: %w", err)
	}
	return marshalJSON(map[string]interface{}{"vm_id": id, "console": console, "note": "Treat the console descriptor as sensitive."})
}

func requiredVMID(args map[string]interface{}) (int, error) {
	value, ok := args["vm_id"].(float64)
	if !ok || value < 1 || value != math.Trunc(value) {
		return 0, fmt.Errorf("vm_id is required and must be a positive integer")
	}
	return int(value), nil
}

func handleVMSSHTest(client *truenas.Client, args map[string]interface{}) (string, error) {
	_ = client
	host, _ := args["host"].(string)
	username, _ := args["username"].(string)
	identityFile, _ := args["identity_file"].(string)
	if host == "" || strings.ContainsAny(host, " \t\r\n/") {
		return "", fmt.Errorf("host is required and must be a hostname or IP address")
	}
	if !safeSSHUser.MatchString(username) {
		return "", fmt.Errorf("username is required and must be a normal Unix username")
	}
	if !filepath.IsAbs(identityFile) {
		return "", fmt.Errorf("identity_file must be an absolute path")
	}
	info, err := os.Stat(identityFile)
	if err != nil {
		return "", fmt.Errorf("identity_file is not readable: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("identity_file permissions are too broad; private keys must not be group/world readable")
	}
	knownHosts, _ := args["known_hosts"].(string)
	commandArgs := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new", "-i", identityFile}
	if knownHosts != "" {
		if !filepath.IsAbs(knownHosts) {
			return "", fmt.Errorf("known_hosts must be an absolute path")
		}
		commandArgs = append(commandArgs, "-o", "UserKnownHostsFile="+knownHosts)
	}
	commandArgs = append(commandArgs, username+"@"+host, "printf", "codex-ssh-ok")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ssh", commandArgs...).CombinedOutput()
	if err != nil {
		return marshalJSON(map[string]interface{}{"success": false, "host": host, "error": strings.TrimSpace(string(output))})
	}
	return marshalJSON(map[string]interface{}{"success": true, "host": host, "username": username, "probe": string(output)})
}
