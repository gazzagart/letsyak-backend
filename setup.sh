#!/usr/bin/env bash
set -euo pipefail

# =============================================================================
# LetsYak Server Setup
# Generates all configuration and secrets for a new deployment.
#
# Usage:
#   ./setup.sh           Production — prompts for domain names and server IP
#   ./setup.sh --local   Local dev  — binds everything to localhost, no TLS
#
# Run once before 'docker compose up -d'.  Re-running overwrites all config.
# =============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# --- Parse flags -------------------------------------------------------------
LOCAL=false
for arg in "$@"; do
    if [[ "$arg" == "--local" ]]; then
        LOCAL=true
    fi
done

echo -e "${CYAN}"
if $LOCAL; then
    echo "╔═══════════════════════════════════════╗"
    echo "║   LetsYak Server Setup (LOCAL DEV)    ║"
    echo "╚═══════════════════════════════════════╝"
else
    echo "╔═══════════════════════════════════════╗"
    echo "║       LetsYak Server Setup            ║"
    echo "╚═══════════════════════════════════════╝"
fi
echo -e "${NC}"

# --- Prerequisites -----------------------------------------------------------
for cmd in docker openssl; do
    if ! command -v "$cmd" &> /dev/null; then
        echo -e "${RED}Error: '$cmd' is required but not installed.${NC}"
        exit 1
    fi
done

if ! docker info &> /dev/null 2>&1; then
    echo -e "${RED}Error: Docker daemon is not running.${NC}"
    exit 1
fi

# --- Guard against re-run ----------------------------------------------------
if [ -f .env ]; then
    echo -e "${YELLOW}Warning: .env already exists. This will overwrite all configuration.${NC}"
    read -p "Continue? (y/N): " confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 0
    fi
fi

# --- Gather configuration ----------------------------------------------------
if $LOCAL; then
    MATRIX_DOMAIN="localhost"
    TURN_DOMAIN="localhost"
    VAULT_API_DOMAIN="localhost:8090"
    VAULT_FILES_DOMAIN="localhost:9000"
    PUBLIC_IP="127.0.0.1"
    PUBLIC_SCHEME="http"
    SYNAPSE_BASEURL="http://localhost:8008/"
    ENABLE_REGISTRATION="true"
    echo -e "${CYAN}Local dev mode — all services bound to localhost.${NC}"
    echo ""
else
    echo -e "${CYAN}Configuration${NC}"
    echo "─────────────"

    read -p "Matrix domain [chat.maybery.app]: " MATRIX_DOMAIN
    MATRIX_DOMAIN=${MATRIX_DOMAIN:-chat.maybery.app}

    read -p "TURN domain [turn.maybery.app]: " TURN_DOMAIN
    TURN_DOMAIN=${TURN_DOMAIN:-turn.maybery.app}

    read -p "Vault API domain [vault.maybery.app]: " VAULT_API_DOMAIN
    VAULT_API_DOMAIN=${VAULT_API_DOMAIN:-vault.maybery.app}

    read -p "Vault files / MinIO domain [files.maybery.app]: " VAULT_FILES_DOMAIN
    VAULT_FILES_DOMAIN=${VAULT_FILES_DOMAIN:-files.maybery.app}

    read -p "Server public IP address: " PUBLIC_IP
    while [ -z "$PUBLIC_IP" ]; do
        read -p "  Public IP (required): " PUBLIC_IP
    done

    PUBLIC_SCHEME="https"
    SYNAPSE_BASEURL="https://${MATRIX_DOMAIN}/"
    ENABLE_REGISTRATION="false"
fi

# --- Generate secrets ---------------------------------------------------------
echo -e "${YELLOW}Generating secrets...${NC}"
POSTGRES_PASSWORD=$(openssl rand -base64 32 | tr -d '/+')
REGISTRATION_SECRET=$(openssl rand -base64 32 | tr -d '/+')
MACAROON_SECRET=$(openssl rand -base64 32 | tr -d '/+')
FORM_SECRET=$(openssl rand -base64 32 | tr -d '/+')
TURN_SECRET=$(openssl rand -base64 32 | tr -d '/+')
MINIO_ACCESS_KEY=$(openssl rand -hex 12)
MINIO_SECRET_KEY=$(openssl rand -base64 32 | tr -d '/+')

# --- Create .env --------------------------------------------------------------
cat > .env << EOF
# LetsYak Server Configuration
# Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
# Mode: $(if $LOCAL; then echo "local"; else echo "production"; fi)

MATRIX_DOMAIN=${MATRIX_DOMAIN}
TURN_DOMAIN=${TURN_DOMAIN}
VAULT_API_DOMAIN=${VAULT_API_DOMAIN}
VAULT_FILES_DOMAIN=${VAULT_FILES_DOMAIN}
PUBLIC_IP=${PUBLIC_IP}

POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
REGISTRATION_SECRET=${REGISTRATION_SECRET}
MACAROON_SECRET=${MACAROON_SECRET}
FORM_SECRET=${FORM_SECRET}
TURN_SECRET=${TURN_SECRET}

MINIO_ACCESS_KEY=${MINIO_ACCESS_KEY}
MINIO_SECRET_KEY=${MINIO_SECRET_KEY}
EOF
chmod 600 .env
echo -e "${GREEN}  ✓ .env${NC}"

# --- Create directories -------------------------------------------------------
mkdir -p synapse/media_store coturn scripts well-known/www

# --- Generate Synapse signing key ---------------------------------------------
echo -e "${YELLOW}Generating Synapse signing key...${NC}"
docker run --rm \
    -v "$(pwd)/synapse:/data" \
    --entrypoint python3 \
    matrixdotorg/synapse:latest \
    -c "
from signedjson.key import generate_signing_key, write_signing_keys
key = generate_signing_key('auto')
with open('/data/signing.key', 'w') as f:
    write_signing_keys(f, [key])
" 2>/dev/null
echo -e "${GREEN}  ✓ synapse/signing.key${NC}"

# --- Build TURN config block (omitted for local dev) -------------------------
if $LOCAL; then
    TURN_BLOCK=""
else
    TURN_BLOCK="
turn_uris:
  - \"turn:${TURN_DOMAIN}:3478?transport=udp\"
  - \"turn:${TURN_DOMAIN}:3478?transport=tcp\"
  - \"turns:${TURN_DOMAIN}:5349?transport=tcp\"
turn_shared_secret: \"${TURN_SECRET}\"
turn_user_lifetime: 1h
turn_allow_guests: false"
fi

# --- Create homeserver.yaml ---------------------------------------------------
cat > synapse/homeserver.yaml << YAML
# LetsYak Synapse Configuration
# Domain: ${MATRIX_DOMAIN}

server_name: "${MATRIX_DOMAIN}"
public_baseurl: "${SYNAPSE_BASEURL}"
pid_file: /data/homeserver.pid
serve_server_wellknown: true

listeners:
  - port: 8008
    tls: false
    type: http
    x_forwarded: true
    bind_addresses: ["0.0.0.0"]
    resources:
      - names: [client, federation]
        compress: false

database:
  name: psycopg2
  args:
    user: synapse
    password: "${POSTGRES_PASSWORD}"
    database: synapse
    host: postgres
    cp_min: 5
    cp_max: 10

redis:
  enabled: true
  host: redis
  port: 6379

log_config: "/data/log.config"

media_store_path: /data/media_store
max_upload_size: 100M

url_preview_enabled: true
url_preview_ip_range_blacklist:
  - "127.0.0.0/8"
  - "10.0.0.0/8"
  - "172.16.0.0/12"
  - "192.168.0.0/16"
  - "100.64.0.0/10"
  - "169.254.0.0/16"
  - "::1/128"
  - "fe80::/10"
  - "fc00::/7"

report_stats: false
enable_registration: ${ENABLE_REGISTRATION}
enable_registration_without_verification: ${ENABLE_REGISTRATION}
allow_public_rooms_without_auth: false
allow_public_rooms_over_federation: false

registration_shared_secret: "${REGISTRATION_SECRET}"
macaroon_secret_key: "${MACAROON_SECRET}"
form_secret: "${FORM_SECRET}"

signing_key_path: "/data/signing.key"
trusted_key_servers:
  - server_name: "matrix.org"
${TURN_BLOCK}
retention:
  enabled: true
  default_policy:
    min_lifetime: 1d
    max_lifetime: 365d

rc_message:
  per_second: 5
  burst_count: 30

rc_login:
  address:
    per_second: 0.15
    burst_count: 5
  account:
    per_second: 0.15
    burst_count: 5
YAML
echo -e "${GREEN}  ✓ synapse/homeserver.yaml${NC}"

# --- Create log config --------------------------------------------------------
cat > synapse/log.config << YAML
version: 1
formatters:
  precise:
    format: '%(asctime)s - %(name)s - %(lineno)d - %(levelname)s - %(request)s - %(message)s'
handlers:
  console:
    class: logging.StreamHandler
    formatter: precise
loggers:
  synapse.storage.SQL:
    level: WARNING
root:
  level: WARNING
  handlers: [console]
disable_existing_loggers: false
YAML
echo -e "${GREEN}  ✓ synapse/log.config${NC}"

# --- Create coturn config (production only) -----------------------------------
if ! $LOCAL; then
    cat > coturn/turnserver.conf << CONF
# LetsYak TURN Server
use-auth-secret
static-auth-secret=${TURN_SECRET}
realm=${MATRIX_DOMAIN}
server-name=${TURN_DOMAIN}

listening-port=3478
tls-listening-port=5349
external-ip=${PUBLIC_IP}

fingerprint
no-multicast-peers
no-cli
stale-nonce=600

min-port=49160
max-port=49200
no-tcp-relay

user-quota=12
total-quota=1200

log-file=stdout
simple-log
CONF
    echo -e "${GREEN}  ✓ coturn/turnserver.conf${NC}"
fi

# --- Create well-known JSON files ---------------------------------------------
if $LOCAL; then
    cat > well-known/www/client.json << JSON
{
  "m.homeserver": {
    "base_url": "http://localhost:8008"
  }
}
JSON
    cat > well-known/www/server.json << JSON
{
  "m.server": "localhost:8008"
}
JSON
else
    cat > well-known/www/client.json << JSON
{
  "m.homeserver": {
    "base_url": "https://${MATRIX_DOMAIN}"
  }
}
JSON
    cat > well-known/www/server.json << JSON
{
  "m.server": "${MATRIX_DOMAIN}:443"
}
JSON
fi
echo -e "${GREEN}  ✓ well-known/www/client.json${NC}"
echo -e "${GREEN}  ✓ well-known/www/server.json${NC}"

# --- Fix ownership for Synapse container (runs as UID 991) -------------------
echo -e "${YELLOW}Setting file permissions...${NC}"
docker run --rm \
    -v "$(pwd)/synapse:/data" \
    --entrypoint sh \
    matrixdotorg/synapse:latest \
    -c "chown -R 991:991 /data" 2>/dev/null
echo -e "${GREEN}  ✓ Permissions set${NC}"

# --- Local: create docker-compose.override.yml --------------------------------
if $LOCAL; then
    cat > docker-compose.override.yml << 'OVERRIDE'
# docker-compose.override.yml — LOCAL DEVELOPMENT
# Auto-generated by ./setup.sh --local — DO NOT COMMIT
#
# Exposes services on localhost ports and replaces the external proxy-network
# with a local bridge so Docker Compose starts without Nginx Proxy Manager.

services:
  synapse:
    ports:
      - "127.0.0.1:8008:8008"

  well-known:
    ports:
      - "127.0.0.1:8080:80"

  vault-api:
    ports:
      - "127.0.0.1:8090:8090"
    environment:
      MINIO_PUBLIC_URL: "http://localhost:9000"
      VAULT_PUBLIC_URL: "http://localhost:8090"

  minio:
    ports:
      - "127.0.0.1:9000:9000"
      - "127.0.0.1:9001:9001"
    command: server /data --console-address ":9001"

  coturn:
    profiles: ["disabled"]

  sygnal:
    profiles: ["disabled"]

  letsyak-web:
    profiles: ["disabled"]

networks:
  proxy-network:
    external: false
    name: letsyak-proxy-local
OVERRIDE
    echo -e "${GREEN}  ✓ docker-compose.override.yml${NC}"
fi

# --- Production: ensure proxy-network exists ---------------------------------
if ! $LOCAL; then
    if ! docker network inspect proxy-network &>/dev/null; then
        echo -e "${YELLOW}Creating proxy-network...${NC}"
        docker network create proxy-network
        echo -e "${GREEN}  ✓ proxy-network created${NC}"
    fi
fi

# --- Summary ------------------------------------------------------------------
echo ""
if $LOCAL; then
    echo -e "${GREEN}╔═══════════════════════════════════════╗"
    echo -e "║     Local Dev Setup Complete!         ║"
    echo -e "╚═══════════════════════════════════════╝${NC}"
    echo ""
    echo "NEXT STEPS:"
    echo ""
    echo "1. Start the stack:"
    echo "   docker compose up -d"
    echo ""
    echo "2. Services available at:"
    echo "   Synapse (Matrix):  http://localhost:8008"
    echo "   Well-known:        http://localhost:8080"
    echo "   Vault API:         http://localhost:8090"
    echo "   MinIO console:     http://localhost:9001"
    echo ""
    echo "3. In the LetsYak app login screen, set homeserver to:"
    echo "   http://localhost:8008"
    echo ""
    echo "4. Create a test user:"
    echo "   ./scripts/create-user.sh alice 'password123'"
    echo "   (Open registration is also enabled — anyone can sign up)"
    echo ""
else
    echo -e "${GREEN}╔═══════════════════════════════════════╗"
    echo -e "║         Setup Complete!               ║"
    echo -e "╚═══════════════════════════════════════╝${NC}"
    echo ""
    echo "NEXT STEPS:"
    echo ""
    echo "1. DNS records (if not already set):"
    echo "   ${MATRIX_DOMAIN}       →  A  ${PUBLIC_IP}  (can be Cloudflare proxied)"
    echo "   ${TURN_DOMAIN}         →  A  ${PUBLIC_IP}  (MUST be DNS only / grey cloud)"
    echo "   ${VAULT_API_DOMAIN}    →  A  ${PUBLIC_IP}  (can be Cloudflare proxied)"
    echo "   ${VAULT_FILES_DOMAIN}  →  A  ${PUBLIC_IP}  (can be Cloudflare proxied)"
    echo ""
    echo "2. Firewall ports:"
    echo "   3478/tcp+udp     TURN"
    echo "   5349/tcp+udp     TURNS (TLS)"
    echo "   49160-49200/udp  TURN relay range"
    echo "   (80/443 already open for your reverse proxy)"
    echo ""
    echo "3. Configure your reverse proxy (NPM) for ${MATRIX_DOMAIN}:"
    echo "   See README.md for exact settings."
    echo ""
    echo "4. Start the stack:"
    echo "   docker compose up -d"
    echo ""
    echo "5. Create admin user:"
    echo "   ./scripts/create-user.sh admin 'YOUR_PASSWORD' --admin"
    echo ""
    echo "6. Point LetsYak app to:"
    echo "   https://${MATRIX_DOMAIN}"
    echo ""
fi
