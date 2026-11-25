# Generic registry and filesystem tools

The MCP exposes reusable TrueNAS primitives for application deployment and host-side setup:

- `query_app_registries` and `query_app_images` are read-only.
- `create_app_registry` and `update_app_registry` accept credentials as secret arguments; passwords are never returned.
- `pull_app_image` returns a task ID and supports private registry authentication.
- `stat_path`, `ensure_directory`, `write_file`, and `set_path_permissions` operate only below `/mnt`.
- `get_app_container_logs` returns a bounded, sanitized log tail, and `wait_for_app_container_log` waits for a literal marker with a bounded timeout. Both are read-only and support app, service, or exact container targeting.
- `generate_ssh_key_pair` creates Ed25519 keys without replacing existing files. The private key is not returned and is written with mode `0600` by default.
- `generate_secret_file` uses the operating system cryptographic random generator and writes protected secret material. It does not reveal the secret unless `reveal` is explicitly requested.

File uploads use TrueNAS's multipart `/_upload/` protocol. Upload contents, registry passwords, API tokens, Compose YAML, and private keys are redacted from API errors, task arguments, task results, and response logging.

Write operations support `dry_run` where applicable. Existing paths and credentials are preserved unless an explicit `overwrite` or destructive operation is requested.

Container logs are received through a temporary TrueNAS middleware subscription. The MCP client unsubscribes on success, timeout, connection loss, and client shutdown. Log payloads are not written to application logs or task state; common bearer credentials, passwords, tokens, API keys, private keys, and GitHub token formats are redacted before output.

For an opt-in live check, set `TRUENAS_URL`, `TRUENAS_API_KEY`, and optionally `CONTAINER_LOG_APP`, `CONTAINER_LOG_PATTERN`, `CONTAINER_LOG_SERVICE`, or `CONTAINER_LOG_CONTAINER_ID`, then run `go run ./cmd/mesh-log-probe`. The command prints only the sanitized structured tool result.
