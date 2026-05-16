#!/usr/bin/env bash
set -euo pipefail

# End-to-end local smoke test for two isolated tenant data-plane stacks.
# Creates temporary tenant directories so generated Synapse config and bind
# mounts do not interfere with the developer's normal local stack.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workspace_root="$(cd "$repo_root/.." && pwd)"

keep_stacks="${KEEP_LETSYAK_SMOKE:-0}"
project_prefix="${LETSYAK_SMOKE_PROJECT_PREFIX:-letsyak-smoke-$(date +%s)}"
smoke_root="${LETSYAK_SMOKE_ROOT:-${TMPDIR:-/tmp}/${project_prefix}}"

tenant_a_project="${project_prefix}-a"
tenant_b_project="${project_prefix}-b"
control_plane_project="${project_prefix}-control-plane"
tenant_a_dir="$smoke_root/local-a"
tenant_b_dir="$smoke_root/local-b"
control_plane_dir="$smoke_root/control-plane"
control_plane_proxy_network="${project_prefix}-proxy-network"

tenant_a_synapse_port="${LETSYAK_SMOKE_A_SYNAPSE_PORT:-18008}"
tenant_a_well_known_port="${LETSYAK_SMOKE_A_WELL_KNOWN_PORT:-18080}"
tenant_a_vault_port="${LETSYAK_SMOKE_A_VAULT_PORT:-18090}"
tenant_a_minio_port="${LETSYAK_SMOKE_A_MINIO_PORT:-19000}"
tenant_a_minio_console_port="${LETSYAK_SMOKE_A_MINIO_CONSOLE_PORT:-19001}"

tenant_b_synapse_port="${LETSYAK_SMOKE_B_SYNAPSE_PORT:-18108}"
tenant_b_well_known_port="${LETSYAK_SMOKE_B_WELL_KNOWN_PORT:-18180}"
tenant_b_vault_port="${LETSYAK_SMOKE_B_VAULT_PORT:-18190}"
tenant_b_minio_port="${LETSYAK_SMOKE_B_MINIO_PORT:-19100}"
tenant_b_minio_console_port="${LETSYAK_SMOKE_B_MINIO_CONSOLE_PORT:-19101}"
control_plane_port="${LETSYAK_SMOKE_CONTROL_PLANE_PORT:-18085}"

smoke_password="${LETSYAK_SMOKE_PASSWORD:-SmokePassw0rd!}"
tenant_a_user="${LETSYAK_SMOKE_A_USER:-smoke_alice}"
tenant_b_user="${LETSYAK_SMOKE_B_USER:-smoke_bob}"
smoke_bind_address="${LETSYAK_SMOKE_BIND_ADDRESS:-127.0.0.1}"
smoke_public_host="${LETSYAK_SMOKE_PUBLIC_HOST:-localhost}"

cleanup() {
    local exit_code=$?
    if [[ "$keep_stacks" != "1" ]]; then
        if [[ -d "$tenant_a_dir" ]]; then
            (cd "$tenant_a_dir" && docker compose -f docker-compose.yml -f docker-compose.override.yml -f docker-compose.tenant-data-plane.yml -p "$tenant_a_project" down -v --remove-orphans >/dev/null 2>&1 || true)
        fi
        if [[ -d "$tenant_b_dir" ]]; then
            (cd "$tenant_b_dir" && docker compose -f docker-compose.yml -f docker-compose.override.yml -f docker-compose.tenant-data-plane.yml -p "$tenant_b_project" down -v --remove-orphans >/dev/null 2>&1 || true)
        fi
        if [[ -d "$control_plane_dir" ]]; then
            (cd "$control_plane_dir" && set -a && . ./.env.smoke && set +a && docker compose -f docker-compose.control-plane.yml -p "$control_plane_project" down -v --remove-orphans >/dev/null 2>&1 || true)
        fi
        docker network rm "$control_plane_proxy_network" >/dev/null 2>&1 || true
        rm -rf "$smoke_root"
    else
        echo "Keeping smoke stacks and files for inspection:"
        echo "  $tenant_a_dir  project=$tenant_a_project"
        echo "  $tenant_b_dir  project=$tenant_b_project"
        echo "  $control_plane_dir  project=$control_plane_project"
    fi
    exit "$exit_code"
}
trap cleanup EXIT

log() {
    printf '\n[%s] %s\n' "$(date +%H:%M:%S)" "$*"
}

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Error: required command not found: $1" >&2
        exit 1
    fi
}

assert_port_free() {
    local port="$1"
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "Error: localhost port $port is already in use." >&2
        echo "Set LETSYAK_SMOKE_*_PORT environment variables to use another range." >&2
        exit 1
    fi
}

wait_for_http_status() {
    local name="$1"
    local url="$2"
    local expected_status="$3"
    local deadline=$((SECONDS + 180))
    local status="000"

    while (( SECONDS < deadline )); do
        status="$(curl -sS -o /dev/null -w '%{http_code}' "$url" || true)"
        if [[ "$status" == "$expected_status" ]]; then
            echo "PASS: $name returned HTTP $status"
            return 0
        fi
        sleep 3
    done

    echo "FAIL: $name expected HTTP $expected_status from $url, got $status" >&2
    return 1
}

assert_response_contains() {
    local name="$1"
    local url="$2"
    local expected="$3"
    local body
    body="$(curl -fsS "$url")"
    if [[ "$body" != *"$expected"* ]]; then
        echo "FAIL: $name response did not contain '$expected'" >&2
        echo "$body" >&2
        return 1
    fi
    echo "PASS: $name contains '$expected'"
}

json_field() {
    local field="$1"
    python3 -c 'import json, sys
field = sys.argv[1]
value = json.load(sys.stdin).get(field, "")
print(value if value is not None else "")' "$field"
}

login_access_token() {
    local homeserver_url="$1"
    local username="$2"
    local response
    response="$(curl -fsS \
        -H 'Content-Type: application/json' \
        -X POST "$homeserver_url/_matrix/client/v3/login" \
        --data "{\"type\":\"m.login.password\",\"identifier\":{\"type\":\"m.id.user\",\"user\":\"$username\"},\"password\":\"$smoke_password\"}")"
    printf '%s' "$response" | json_field access_token
}

copy_tenant_dir() {
    local destination="$1"
    mkdir -p "$destination"
    rsync -a --delete \
        --exclude '.env' \
        --exclude 'docker-compose.override.yml' \
        --exclude 'synapse/homeserver.yaml' \
        --exclude 'synapse/log.config' \
        --exclude 'synapse/signing.key' \
        --exclude 'synapse/media_store' \
        --exclude 'coturn/turnserver.conf' \
        "$repo_root/" "$destination/"
}

write_control_plane_config() {
    local destination="$1/control-plane/config/tenants.smoke.generated.json"
    cat > "$destination" <<JSON
{
  "workspaces": [
    {
      "id": "local-a",
      "slug": "local-a",
      "display_name": "LetsYak Smoke A",
      "status": "active",
      "email_domains": ["a.example.com"],
    "homeserver_url": "http://${smoke_public_host}:${tenant_a_synapse_port}",
    "vault_api_url": "http://${smoke_public_host}:${tenant_a_vault_port}",
      "isolation_tier": "dedicated",
      "security_mode": "easy_e2ee",
      "login_methods": ["password"],
      "branding": {"primary_color": "#5625BA", "secondary_color": "#41A2BC"},
      "features": {"vault": true, "organisation_admin": true}
    },
    {
      "id": "local-b",
      "slug": "local-b",
      "display_name": "LetsYak Smoke B",
      "status": "active",
      "email_domains": ["b.example.com"],
    "homeserver_url": "http://${smoke_public_host}:${tenant_b_synapse_port}",
    "vault_api_url": "http://${smoke_public_host}:${tenant_b_vault_port}",
      "isolation_tier": "dedicated",
      "security_mode": "strict",
      "login_methods": ["password"],
      "branding": {"primary_color": "#0F766E", "secondary_color": "#F59E0B"},
      "features": {"vault": true, "organisation_admin": true}
    }
  ]
}
JSON
}

write_tenant_env() {
    local destination="$1"
    local stack_name="$2"
    local matrix_domain="$3"
    local synapse_port="$4"
    local well_known_port="$5"
    local vault_port="$6"
    local minio_port="$7"
    local minio_console_port="$8"

    cat > "$destination/.env.smoke" <<ENV
TENANT_STACK_NAME=${stack_name}
MATRIX_DOMAIN=${matrix_domain}
LOCAL_BIND_ADDRESS=${smoke_bind_address}
LOCAL_PUBLIC_HOST=${smoke_public_host}
SYNAPSE_HTTP_PORT=${synapse_port}
WELL_KNOWN_HTTP_PORT=${well_known_port}
CONTROL_PLANE_HTTP_PORT=${control_plane_port}
VAULT_API_HTTP_PORT=${vault_port}
MINIO_API_PORT=${minio_port}
MINIO_CONSOLE_PORT=${minio_console_port}
CONTROL_PLANE_TENANT_CONFIG=./control-plane/config/tenants.smoke.generated.json
LOCAL_CORS_ALLOWED_ORIGINS=http://localhost:*,http://127.0.0.1:*
ENV
}

write_control_plane_env() {
    local destination="$1"
    cat > "$destination/.env.smoke" <<ENV
CONTROL_PLANE_STACK_NAME=letsyak-smoke-control-plane
CONTROL_PLANE_TENANT_CONFIG=./control-plane/config/tenants.smoke.generated.json
CONTROL_PLANE_HTTP_PORT=${control_plane_port}
CONTROL_PLANE_CORS_ALLOWED_ORIGINS=*
LOCAL_BIND_ADDRESS=${smoke_bind_address}
PROXY_NETWORK_NAME=${control_plane_proxy_network}
ENV
}

tenant_compose() {
    local directory="$1"
    local project="$2"
    shift 2
    (cd "$directory" && docker compose \
        -f docker-compose.yml \
        -f docker-compose.override.yml \
        -f docker-compose.tenant-data-plane.yml \
        -p "$project" \
        "$@")
}

control_plane_compose() {
    (cd "$control_plane_dir" && set -a && . ./.env.smoke && set +a && docker compose \
        -f docker-compose.control-plane.yml \
        -p "$control_plane_project" \
        "$@")
}

run_setup() {
    local directory="$1"
    (cd "$directory" && set -a && . ./.env.smoke && set +a && ./setup.sh --local >/tmp/letsyak-smoke-setup.log)
}

register_user() {
    local directory="$1"
    local project="$2"
    local username="$3"
    tenant_compose "$directory" "$project" exec -T synapse register_new_matrix_user \
        -c /data/homeserver.yaml \
        http://localhost:8008 \
        -u "$username" \
        -p "$smoke_password" \
        --no-admin \
        --exists-ok >/dev/null
    echo "PASS: registered $username in $project"
}

verify_vault_token() {
    local name="$1"
    local vault_url="$2"
    local token="$3"
    local expected_status="$4"
    local status
    status="$(curl -sS -o /dev/null -w '%{http_code}' \
        -H "Authorization: Bearer $token" \
        "$vault_url/api/v1/quota" || true)"
    if [[ "$status" != "$expected_status" ]]; then
        echo "FAIL: $name expected HTTP $expected_status, got $status" >&2
        return 1
    fi
    echo "PASS: $name returned HTTP $status"
}

provision_vault_user() {
    local name="$1"
    local vault_url="$2"
    local token="$3"
    local status
    status="$(curl -sS -o /dev/null -w '%{http_code}' \
        -H "Authorization: Bearer $token" \
        -X POST \
        "$vault_url/api/v1/auth/provision" || true)"
    if [[ "$status" != "200" ]]; then
        echo "FAIL: $name expected HTTP 200, got $status" >&2
        return 1
    fi
    echo "PASS: $name returned HTTP $status"
}

main() {
    require_command curl
    require_command docker
    require_command lsof
    require_command openssl
    require_command python3
    require_command rsync

    if ! docker info >/dev/null 2>&1; then
        echo "Error: Docker daemon is not running." >&2
        exit 1
    fi

    for port in \
        "$control_plane_port" \
        "$tenant_a_synapse_port" "$tenant_a_well_known_port" "$tenant_a_vault_port" "$tenant_a_minio_port" "$tenant_a_minio_console_port" \
        "$tenant_b_synapse_port" "$tenant_b_well_known_port" "$tenant_b_vault_port" "$tenant_b_minio_port" "$tenant_b_minio_console_port"; do
        assert_port_free "$port"
    done

    log "Preparing temporary tenant directories under $smoke_root"
    rm -rf "$smoke_root"
    mkdir -p "$smoke_root"
    copy_tenant_dir "$control_plane_dir"
    copy_tenant_dir "$tenant_a_dir"
    copy_tenant_dir "$tenant_b_dir"
    write_control_plane_config "$control_plane_dir"
    write_control_plane_config "$tenant_a_dir"
    write_control_plane_config "$tenant_b_dir"
    write_control_plane_env "$control_plane_dir"
    write_tenant_env "$tenant_a_dir" letsyak-smoke-a smoke-a.letsyak.test "$tenant_a_synapse_port" "$tenant_a_well_known_port" "$tenant_a_vault_port" "$tenant_a_minio_port" "$tenant_a_minio_console_port"
    write_tenant_env "$tenant_b_dir" letsyak-smoke-b smoke-b.letsyak.test "$tenant_b_synapse_port" "$tenant_b_well_known_port" "$tenant_b_vault_port" "$tenant_b_minio_port" "$tenant_b_minio_console_port"

    log "Generating tenant configs"
    run_setup "$tenant_a_dir"
    run_setup "$tenant_b_dir"

    log "Validating Compose config"
    docker network inspect "$control_plane_proxy_network" >/dev/null 2>&1 || docker network create "$control_plane_proxy_network" >/dev/null
    control_plane_compose config --quiet
    tenant_compose "$tenant_a_dir" "$tenant_a_project" config --quiet
    tenant_compose "$tenant_b_dir" "$tenant_b_project" config --quiet
    echo "PASS: Compose config valid for shared control-plane and both tenants"

    log "Starting shared control-plane and tenant stacks"
    control_plane_compose up -d --build
    tenant_compose "$tenant_a_dir" "$tenant_a_project" up -d --build
    tenant_compose "$tenant_b_dir" "$tenant_b_project" up -d --build

    log "Waiting for tenant endpoints"
    wait_for_http_status "tenant A Synapse" "http://127.0.0.1:${tenant_a_synapse_port}/_matrix/client/versions" 200
    wait_for_http_status "tenant B Synapse" "http://127.0.0.1:${tenant_b_synapse_port}/_matrix/client/versions" 200
    wait_for_http_status "tenant A Vault unauthenticated API" "http://127.0.0.1:${tenant_a_vault_port}/api/v1/quota" 401
    wait_for_http_status "tenant B Vault unauthenticated API" "http://127.0.0.1:${tenant_b_vault_port}/api/v1/quota" 401
    wait_for_http_status "tenant A MinIO ready" "http://127.0.0.1:${tenant_a_minio_port}/minio/health/ready" 200
    wait_for_http_status "tenant B MinIO ready" "http://127.0.0.1:${tenant_b_minio_port}/minio/health/ready" 200
    wait_for_http_status "shared control-plane" "http://127.0.0.1:${control_plane_port}/api/v1/workspaces/resolve?slug=local-a" 200

    log "Checking shared control-plane workspace routing"
    assert_response_contains "shared control-plane local-a" "http://127.0.0.1:${control_plane_port}/api/v1/workspaces/resolve?slug=local-a" "http://${smoke_public_host}:${tenant_a_synapse_port}"
    assert_response_contains "shared control-plane local-b" "http://127.0.0.1:${control_plane_port}/api/v1/workspaces/resolve?slug=local-b" "http://${smoke_public_host}:${tenant_b_synapse_port}"

    log "Creating Matrix users and testing Vault auth"
    register_user "$tenant_a_dir" "$tenant_a_project" "$tenant_a_user"
    register_user "$tenant_b_dir" "$tenant_b_project" "$tenant_b_user"

    tenant_a_token="$(login_access_token "http://127.0.0.1:${tenant_a_synapse_port}" "$tenant_a_user")"
    tenant_b_token="$(login_access_token "http://127.0.0.1:${tenant_b_synapse_port}" "$tenant_b_user")"

    if [[ -z "$tenant_a_token" || -z "$tenant_b_token" ]]; then
        echo "FAIL: Matrix login did not return access tokens" >&2
        exit 1
    fi
    echo "PASS: Matrix login returned access tokens for both tenants"

    verify_vault_token "tenant A quota before provisioning" "http://127.0.0.1:${tenant_a_vault_port}" "$tenant_a_token" 404
    verify_vault_token "tenant B quota before provisioning" "http://127.0.0.1:${tenant_b_vault_port}" "$tenant_b_token" 404
    provision_vault_user "tenant A Vault provision" "http://127.0.0.1:${tenant_a_vault_port}" "$tenant_a_token"
    provision_vault_user "tenant B Vault provision" "http://127.0.0.1:${tenant_b_vault_port}" "$tenant_b_token"
    verify_vault_token "tenant A quota after provisioning" "http://127.0.0.1:${tenant_a_vault_port}" "$tenant_a_token" 200
    verify_vault_token "tenant B quota after provisioning" "http://127.0.0.1:${tenant_b_vault_port}" "$tenant_b_token" 200
    verify_vault_token "tenant A token rejected by tenant B Vault" "http://127.0.0.1:${tenant_b_vault_port}" "$tenant_a_token" 401

    log "Two-tenant smoke test passed"
}

main "$@"