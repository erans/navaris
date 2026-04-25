#!/usr/bin/env bash
set -euo pipefail

navarisd_bin="${NAVARISD_BIN:-/usr/local/bin/navarisd}"
listen="${NAVARIS_LISTEN:-:8080}"
db_path="${NAVARIS_DB_PATH:-/var/lib/navaris/navaris.db}"
log_level="${NAVARIS_LOG_LEVEL:-info}"

args=(
    --listen="$listen"
    --db-path="$db_path"
    --log-level="$log_level"
)

add_string_flag() {
    local flag="$1"
    local value="$2"
    if [ -n "$value" ]; then
        args+=("${flag}=${value}")
    fi
}

add_bool_flag() {
    local flag="$1"
    local value="$2"
    if [ -n "$value" ]; then
        args+=("${flag}=${value}")
    fi
}

add_string_flag --auth-token "${NAVARIS_AUTH_TOKEN:-}"
add_string_flag --incus-socket "${NAVARIS_INCUS_SOCKET:-}"
add_string_flag --gc-interval "${NAVARIS_GC_INTERVAL:-}"
add_string_flag --concurrency "${NAVARIS_CONCURRENCY:-}"
add_string_flag --firecracker-bin "${NAVARIS_FIRECRACKER_BIN:-}"
add_string_flag --jailer-bin "${NAVARIS_JAILER_BIN:-}"
add_string_flag --kernel-path "${NAVARIS_KERNEL_PATH:-}"
add_string_flag --image-dir "${NAVARIS_IMAGE_DIR:-}"
add_string_flag --chroot-base "${NAVARIS_CHROOT_BASE:-}"
add_string_flag --host-interface "${NAVARIS_HOST_INTERFACE:-}"
add_string_flag --snapshot-dir "${NAVARIS_SNAPSHOT_DIR:-}"
add_bool_flag --enable-jailer "${NAVARIS_ENABLE_JAILER:-}"
add_string_flag --firecracker-default-vcpu "${NAVARIS_FIRECRACKER_DEFAULT_VCPU:-}"
add_string_flag --firecracker-default-memory-mb "${NAVARIS_FIRECRACKER_DEFAULT_MEMORY_MB:-}"
add_string_flag --storage-mode "${NAVARIS_STORAGE_MODE:-}"
add_bool_flag --incus-strict-pool-cow "${NAVARIS_INCUS_STRICT_POOL_COW:-}"
add_string_flag --otlp-endpoint "${NAVARIS_OTLP_ENDPOINT:-}"
add_string_flag --otlp-protocol "${NAVARIS_OTLP_PROTOCOL:-}"
add_string_flag --service-name "${NAVARIS_SERVICE_NAME:-}"
add_string_flag --ui-password "${NAVARIS_UI_PASSWORD:-}"
add_string_flag --ui-session-key "${NAVARIS_UI_SESSION_KEY:-}"
add_string_flag --ui-session-ttl "${NAVARIS_UI_SESSION_TTL:-}"
add_bool_flag --mcp-enabled "${NAVARIS_MCP_ENABLED:-}"
add_bool_flag --mcp-read-only "${NAVARIS_MCP_READ_ONLY:-}"
add_string_flag --mcp-path "${NAVARIS_MCP_PATH:-}"
add_string_flag --mcp-max-timeout "${NAVARIS_MCP_MAX_TIMEOUT:-}"

mkdir -p "$(dirname "$db_path")"

exec "$navarisd_bin" "${args[@]}"
