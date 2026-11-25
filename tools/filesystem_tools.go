package tools

import (
	"bytes"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

const maxRemoteFileBytes = 16 * 1024 * 1024

func registerFilesystemTools(r *Registry) {
	r.tools["stat_path"] = Tool{Definition: mcp.Tool{Name: "stat_path", Description: "Inspect a TrueNAS filesystem path without changing it.", InputSchema: objectSchema(map[string]interface{}{"path": map[string]interface{}{"type": "string"}})}, Handler: handleStatPath}
	r.tools["ensure_directory"] = Tool{Definition: mcp.Tool{Name: "ensure_directory", Description: "Create a directory under /mnt if it does not exist, with an optional mode.", InputSchema: objectSchema(map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "mode": map[string]interface{}{"type": "string", "default": "755"}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: r.handleEnsureDirectory}
	r.tools["write_file"] = Tool{Definition: mcp.Tool{Name: "write_file", Description: "Write a bounded UTF-8 or base64 file under /mnt using TrueNAS file upload. Contents are never returned or logged.", InputSchema: objectSchema(map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "content": map[string]interface{}{"type": "string"}, "encoding": map[string]interface{}{"type": "string", "enum": []string{"utf8", "base64"}, "default": "utf8"}, "mode": map[string]interface{}{"type": "string", "default": "600"}, "overwrite": map[string]interface{}{"type": "boolean", "default": false}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: r.handleWriteFile}
	r.tools["set_path_permissions"] = Tool{Definition: mcp.Tool{Name: "set_path_permissions", Description: "Set ownership and/or mode on a TrueNAS filesystem path under /mnt.", InputSchema: objectSchema(map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "uid": map[string]interface{}{"type": "integer"}, "gid": map[string]interface{}{"type": "integer"}, "mode": map[string]interface{}{"type": "string"}, "recursive": map[string]interface{}{"type": "boolean", "default": false}, "traverse": map[string]interface{}{"type": "boolean", "default": false}, "stripacl": map[string]interface{}{"type": "boolean", "default": false}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: r.handleSetPathPermissions}
	r.tools["generate_ssh_key_pair"] = Tool{Definition: mcp.Tool{Name: "generate_ssh_key_pair", Description: "Generate a dedicated Ed25519 key pair under /mnt without replacing existing files. The private key is never returned.", InputSchema: objectSchema(map[string]interface{}{"private_path": map[string]interface{}{"type": "string"}, "public_path": map[string]interface{}{"type": "string"}, "comment": map[string]interface{}{"type": "string", "default": "truenas-mcp"}, "uid": map[string]interface{}{"type": "integer"}, "gid": map[string]interface{}{"type": "integer"}, "overwrite": map[string]interface{}{"type": "boolean", "default": false}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: handleGenerateSSHKeyPair}
	r.tools["generate_secret_file"] = Tool{Definition: mcp.Tool{Name: "generate_secret_file", Description: "Generate cryptographically secure random data in a protected TrueNAS file. The secret is not returned unless reveal is explicitly true.", InputSchema: objectSchema(map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "bytes": map[string]interface{}{"type": "integer", "default": 32, "minimum": 32, "maximum": 4096}, "encoding": map[string]interface{}{"type": "string", "enum": []string{"hex", "base64url"}, "default": "hex"}, "mode": map[string]interface{}{"type": "string", "default": "600"}, "uid": map[string]interface{}{"type": "integer"}, "gid": map[string]interface{}{"type": "integer"}, "overwrite": map[string]interface{}{"type": "boolean", "default": false}, "reveal": map[string]interface{}{"type": "boolean", "default": false}, "stripacl": map[string]interface{}{"type": "boolean", "default": false}, "dry_run": map[string]interface{}{"type": "boolean", "default": false}})}, Handler: handleGenerateSecretFile}
}

func handleStatPath(client *truenas.Client, args map[string]interface{}) (string, error) {
	path, err := safeMountPath(args, "path")
	if err != nil {
		return "", err
	}
	result, err := client.Call("filesystem.stat", path)
	if err != nil {
		return "", err
	}
	return safeJSON(result)
}

func (r *Registry) handleEnsureDirectory(client *truenas.Client, args map[string]interface{}) (string, error) {
	path, err := safeMountPath(args, "path")
	if err != nil {
		return "", err
	}
	mode, err := parseFileMode(optionalStringDefault(args, "mode", "755"))
	if err != nil {
		return "", err
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "ensure_directory", "dry_run": true, "path": path, "mode": fmt.Sprintf("%03o", mode)})
	}
	if result, statErr := client.Call("filesystem.stat", path); statErr == nil {
		kind := filesystemType(result)
		if kind != "" && !strings.Contains(strings.ToLower(kind), "dir") {
			return "", fmt.Errorf("path exists but is not a directory")
		}
		return marshalJSON(map[string]interface{}{"path": path, "created": false, "already_exists": true})
	} else if !isMissingPathError(statErr) {
		return "", fmt.Errorf("cannot inspect directory path: %w", statErr)
	}
	result, err := client.Call("filesystem.mkdir", map[string]interface{}{
		"path": path,
		"options": map[string]interface{}{
			"mode":              fmt.Sprintf("%03o", mode),
			"raise_chmod_error": true,
		},
	})
	if err != nil {
		return "", err
	}
	jobID, err := parseAppJobID(result, "filesystem.mkdir")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("ensure_directory", args, jobID, genericTaskTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"task_id": task.TaskID, "job_id": jobID, "path": path, "created": true})
}

func (r *Registry) handleWriteFile(client *truenas.Client, args map[string]interface{}) (string, error) {
	path, err := safeMountPath(args, "path")
	if err != nil {
		return "", err
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	mode, err := parseFileMode(optionalStringDefault(args, "mode", "600"))
	if err != nil {
		return "", err
	}
	contents, err := decodeFileContent(content, optionalStringDefault(args, "encoding", "utf8"))
	if err != nil {
		return "", err
	}
	if len(contents) > maxRemoteFileBytes {
		return "", fmt.Errorf("content exceeds the %d-byte safety limit", maxRemoteFileBytes)
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "write_file", "dry_run": true, "path": path, "bytes": len(contents), "mode": fmt.Sprintf("%03o", mode), "overwrite": boolArg(args, "overwrite")})
	}
	if _, statErr := client.Call("filesystem.stat", path); statErr == nil && !boolArg(args, "overwrite") {
		return "", fmt.Errorf("path already exists; set overwrite=true explicitly")
	} else if statErr != nil && !isMissingPathError(statErr) {
		return "", fmt.Errorf("cannot inspect file path: %w", statErr)
	}
	jobID, err := client.UploadFile("filesystem.put", []interface{}{path, map[string]interface{}{"mode": mode}}, filepath.Base(path), contents)
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("write_file", args, jobID, genericTaskTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"task_id": task.TaskID, "job_id": jobID, "path": path, "bytes": len(contents)})
}

func (r *Registry) handleSetPathPermissions(client *truenas.Client, args map[string]interface{}) (string, error) {
	path, err := safeMountPath(args, "path")
	if err != nil {
		return "", err
	}
	options := map[string]interface{}{}
	for _, key := range []string{"uid", "gid"} {
		if value, ok := integerArg(args, key); ok {
			options[key] = value
		} else if _, supplied := args[key]; supplied {
			return "", fmt.Errorf("%s must be a non-negative integer", key)
		}
	}
	if modeText, ok := args["mode"]; ok {
		mode, modeErr := parseFileMode(fmt.Sprint(modeText))
		if modeErr != nil {
			return "", modeErr
		}
		options["mode"] = fmt.Sprintf("%03o", mode)
	}
	if len(options) == 0 {
		return "", fmt.Errorf("at least one of uid, gid, or mode is required")
	}
	options["options"] = map[string]interface{}{"recursive": boolArg(args, "recursive"), "traverse": boolArg(args, "traverse"), "stripacl": boolArg(args, "stripacl")}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "set_path_permissions", "dry_run": true, "path": path, "options": options})
	}
	options["path"] = path
	result, err := client.Call("filesystem.setperm", options)
	if err != nil {
		return "", err
	}
	jobID, err := parseAppJobID(result, "filesystem.setperm")
	if err != nil {
		return "", err
	}
	task, err := r.taskManager.CreateJobTask("set_path_permissions", args, jobID, genericTaskTTL)
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]interface{}{"task_id": task.TaskID, "job_id": jobID, "path": path})
}

func handleGenerateSSHKeyPair(client *truenas.Client, args map[string]interface{}) (string, error) {
	privatePath, err := safeMountPath(args, "private_path")
	if err != nil {
		return "", err
	}
	publicPath, err := safeMountPath(args, "public_path")
	if err != nil {
		return "", err
	}
	if privatePath == publicPath {
		return "", fmt.Errorf("private_path and public_path must differ")
	}
	privateStatErr := error(nil)
	if _, privateStatErr = client.Call("filesystem.stat", privatePath); privateStatErr != nil && !isMissingPathError(privateStatErr) {
		return "", fmt.Errorf("cannot inspect private key path: %w", privateStatErr)
	}
	publicStatErr := error(nil)
	if _, publicStatErr = client.Call("filesystem.stat", publicPath); publicStatErr != nil && !isMissingPathError(publicStatErr) {
		return "", fmt.Errorf("cannot inspect public key path: %w", publicStatErr)
	}
	// A previous attempt may have uploaded the private key before failing to
	// apply its permissions. Never replace that key; derive and restore only
	// the missing public half.
	if privateStatErr == nil && publicStatErr != nil && !boolArg(args, "overwrite") {
		privateContents, downloadErr := client.DownloadFile("filesystem.get", []interface{}{privatePath}, filepath.Base(privatePath))
		if downloadErr != nil {
			return "", fmt.Errorf("failed to read existing private key for public-key derivation: %w", downloadErr)
		}
		privateKey, parseErr := parseEd25519PrivateKey(privateContents)
		if parseErr != nil {
			return "", parseErr
		}
		publicKey := privateKey.Public().(ed25519.PublicKey)
		publicText := authorizedEd25519Key(publicKey, optionalStringDefault(args, "comment", "truenas-mcp"))
		if err := setRemotePerms(client, privatePath, 0600, args); err != nil {
			return "", fmt.Errorf("failed to secure existing private key: %w", err)
		}
		if err := uploadAndSetPerms(client, publicPath, filepath.Base(publicPath), []byte(publicText), 0644, args); err != nil {
			return "", fmt.Errorf("failed to write derived public key: %w", err)
		}
		return marshalJSON(map[string]interface{}{"private_path": privatePath, "public_path": publicPath, "public_key": strings.TrimSpace(publicText), "comment": optionalStringDefault(args, "comment", "truenas-mcp"), "private_mode": "0600", "public_mode": "0644", "recovered_existing_private": true})
	}
	if err := ensureAbsentOrOverwrite(client, privatePath, boolArg(args, "overwrite")); err != nil {
		return "", err
	}
	if err := ensureAbsentOrOverwrite(client, publicPath, boolArg(args, "overwrite")); err != nil {
		return "", err
	}
	comment := optionalStringDefault(args, "comment", "truenas-mcp")
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "generate_ssh_key_pair", "dry_run": true, "private_path": privatePath, "public_path": publicPath, "comment": comment, "overwrite": boolArg(args, "overwrite")})
	}
	publicKey, privateKey, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to generate Ed25519 key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to encode private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicText := authorizedEd25519Key(publicKey, comment)
	if err := uploadAndSetPerms(client, privatePath, filepath.Base(privatePath), privatePEM, 0600, args); err != nil {
		return "", fmt.Errorf("failed to write private key: %w", err)
	}
	if err := uploadAndSetPerms(client, publicPath, filepath.Base(publicPath), []byte(publicText), 0644, args); err != nil {
		return "", fmt.Errorf("failed to write public key: %w", err)
	}
	return marshalJSON(map[string]interface{}{"private_path": privatePath, "public_path": publicPath, "public_key": strings.TrimSpace(publicText), "comment": comment, "private_mode": "0600", "public_mode": "0644"})
}

func parseEd25519PrivateKey(contents []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(contents)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("existing private key is not a PKCS#8 PEM Ed25519 key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse existing private key: %w", err)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("existing private key is not Ed25519")
	}
	return privateKey, nil
}

func handleGenerateSecretFile(client *truenas.Client, args map[string]interface{}) (string, error) {
	path, err := safeMountPath(args, "path")
	if err != nil {
		return "", err
	}
	length := 32
	if value, ok := integerArg(args, "bytes"); ok {
		length = value
	}
	if length < 32 || length > 4096 {
		return "", fmt.Errorf("bytes must be between 32 and 4096")
	}
	mode, err := parseFileMode(optionalStringDefault(args, "mode", "600"))
	if err != nil {
		return "", err
	}
	if err := ensureAbsentOrOverwrite(client, path, boolArg(args, "overwrite")); err != nil {
		return "", err
	}
	if boolArg(args, "dry_run") {
		return marshalJSON(map[string]interface{}{"tool": "generate_secret_file", "dry_run": true, "path": path, "bytes": length, "mode": fmt.Sprintf("%03o", mode)})
	}
	randomBytes := make([]byte, length)
	if _, err := cryptorand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}
	encodingName := optionalStringDefault(args, "encoding", "hex")
	secret := ""
	switch encodingName {
	case "hex":
		secret = hex.EncodeToString(randomBytes)
	case "base64url":
		secret = base64.RawURLEncoding.EncodeToString(randomBytes)
	default:
		return "", fmt.Errorf("encoding must be hex or base64url")
	}
	if err := uploadAndSetPerms(client, path, filepath.Base(path), []byte(secret+"\n"), mode, args); err != nil {
		return "", err
	}
	response := map[string]interface{}{"path": path, "bytes": length, "encoding": encodingName, "mode": fmt.Sprintf("%03o", mode)}
	if boolArg(args, "reveal") {
		response["secret"] = secret
	}
	return marshalJSON(response)
}

func safeMountPath(args map[string]interface{}, key string) (string, error) {
	path, err := requiredString(args, key)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	if clean != path || !strings.HasPrefix(clean, "/mnt/") || clean == "/mnt" || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%s must be a clean path below /mnt", key)
	}
	for _, part := range strings.Split(strings.TrimPrefix(clean, "/"), "/") {
		if part == ".." || part == "." || part == "" {
			return "", fmt.Errorf("%s contains an unsafe path component", key)
		}
	}
	return clean, nil
}

func parseFileMode(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("mode is required")
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil || parsed > 0777 {
		return 0, fmt.Errorf("mode must be an octal permission between 000 and 777")
	}
	return int(parsed), nil
}

func decodeFileContent(content, encodingName string) ([]byte, error) {
	switch strings.ToLower(encodingName) {
	case "utf8", "":
		return []byte(content), nil
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 content: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("encoding must be utf8 or base64")
	}
}

func integerArg(args map[string]interface{}, key string) (int, bool) {
	switch value := args[key].(type) {
	case int:
		return value, value >= 0
	case float64:
		return int(value), value >= 0 && value == float64(int(value))
	default:
		return 0, false
	}
}

func optionalStringDefault(args map[string]interface{}, key, fallback string) string {
	if value := optionalString(args, key); value != "" {
		return value
	}
	return fallback
}

func filesystemType(raw json.RawMessage) string {
	var value map[string]interface{}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, key := range []string{"type", "file_type"} {
		if text, ok := value[key].(string); ok {
			return text
		}
	}
	return ""
}

func isMissingPathError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "enoent") || strings.Contains(message, "not found") || strings.Contains(message, "no such file")
}

func ensureAbsentOrOverwrite(client *truenas.Client, path string, overwrite bool) error {
	if _, err := client.Call("filesystem.stat", path); err == nil && !overwrite {
		return fmt.Errorf("path already exists: %s; set overwrite=true explicitly", path)
	} else if err != nil && !isMissingPathError(err) {
		return fmt.Errorf("cannot inspect path %s: %w", path, err)
	}
	return nil
}

func uploadAndSetPerms(client *truenas.Client, path, filename string, contents []byte, mode int, args map[string]interface{}) error {
	jobID, err := client.UploadFile("filesystem.put", []interface{}{path, map[string]interface{}{"mode": mode}}, filename, contents)
	if err != nil {
		return err
	}
	if err := waitForTrueNASJob(client, jobID, 2*time.Minute); err != nil {
		return fmt.Errorf("upload job %d failed: %w", jobID, err)
	}
	permOptions := map[string]interface{}{"path": path, "mode": fmt.Sprintf("%03o", mode), "options": map[string]interface{}{"stripacl": boolArg(args, "stripacl")}}
	if uid, ok := integerArg(args, "uid"); ok {
		permOptions["uid"] = uid
	}
	if gid, ok := integerArg(args, "gid"); ok {
		permOptions["gid"] = gid
	}
	result, err := client.Call("filesystem.setperm", permOptions)
	if err != nil {
		return err
	}
	permJobID, err := parseAppJobID(result, "filesystem.setperm")
	if err != nil {
		return err
	}
	return waitForTrueNASJob(client, permJobID, 2*time.Minute)
}

func setRemotePerms(client *truenas.Client, path string, mode int, args map[string]interface{}) error {
	permOptions := map[string]interface{}{"path": path, "mode": fmt.Sprintf("%03o", mode), "options": map[string]interface{}{"stripacl": boolArg(args, "stripacl")}}
	if uid, ok := integerArg(args, "uid"); ok {
		permOptions["uid"] = uid
	}
	if gid, ok := integerArg(args, "gid"); ok {
		permOptions["gid"] = gid
	}
	result, err := client.Call("filesystem.setperm", permOptions)
	if err != nil {
		return err
	}
	jobID, err := parseAppJobID(result, "filesystem.setperm")
	if err != nil {
		return err
	}
	return waitForTrueNASJob(client, jobID, 2*time.Minute)
}

func waitForTrueNASJob(client *truenas.Client, jobID int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := client.Call("core.get_jobs", []interface{}{[]interface{}{"id", "=", jobID}})
		if err == nil {
			var jobs []map[string]interface{}
			if json.Unmarshal(result, &jobs) == nil && len(jobs) > 0 {
				state, _ := jobs[0]["state"].(string)
				switch state {
				case "SUCCESS":
					return nil
				case "FAILED", "ABORTED":
					return fmt.Errorf("TrueNAS job state %s", state)
				}
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for TrueNAS job %d", jobID)
		}
	}
}

func authorizedEd25519Key(publicKey ed25519.PublicKey, comment string) string {
	var wire bytes.Buffer
	writeSSHString(&wire, []byte("ssh-ed25519"))
	writeSSHString(&wire, []byte(publicKey))
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire.Bytes()) + " " + comment + "\n"
}

func writeSSHString(buffer *bytes.Buffer, value []byte) {
	_ = binary.Write(buffer, binary.BigEndian, uint32(len(value)))
	_, _ = buffer.Write(value)
}
