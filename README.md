# LetsYak Server

Portable Matrix homeserver stack for the LetsYak platform.
Runs behind Nginx Proxy Manager (NPM). Designed for per-client deployments.

## Architecture

```
Internet
  │
  ├─ 80/443 ──▶  Nginx Proxy Manager (existing, in C:\docker)
  │                ├── chat.maybery.app ──▶ letsyak-synapse:8008
  │                │     /.well-known/*  ──▶ letsyak-well-known:80
  │                └── (other sites: jellyfin, website, etc.)
  │
  ├─ 3478 ────▶  Coturn (TURN/STUN for voice/video NAT traversal)
  ├─ 5349 ────▶  Coturn (TURNS - TLS)
  └─ 49160-49200/udp ▶ Coturn relay ports

Internal Docker network (not exposed):
  ├── Synapse (Matrix homeserver)  ── proxy-network + letsyak-internal
  ├── PostgreSQL 16                ── letsyak-internal
  ├── Redis 7                      ── letsyak-internal
  ├── Control plane                 ── proxy-network
  └── Well-known (nginx)           ── proxy-network
```

## Prerequisites

- Docker Engine + Docker Compose v2
- Nginx Proxy Manager running on the `proxy-network` Docker network
- Domain name with DNS control (Cloudflare)
- Ports 3478, 5349, 49160-49200/udp open in firewall (80/443 already handled by NPM)

### PowerShell checks on Apple Silicon macOS

The PowerShell setup script can be syntax-checked locally on a Mac without a Windows machine:

```bash
bash scripts/check-setup-ps1.sh
```

The checker uses native `pwsh` when it is installed. On Apple Silicon, Homebrew installs a native macOS build. If `pwsh` is missing but Docker Desktop is running, the checker falls back to Microsoft's official amd64 PowerShell container through Docker Desktop emulation.

To install native PowerShell instead of using Docker:

```bash
brew install --cask powershell
pwsh -NoLogo -NoProfile
```

---

## Local Development

Engineers can run the full backend stack locally with a single command. No reverse proxy or DNS is needed.

### 1. Clone and set up

```bash
git clone <letsyak-server-repo>
cd letsyak-server

# Generate config, secrets, and the local Docker Compose override
./setup.sh --local
```

This creates:
- `.env` with auto-generated secrets and `localhost` domains
- `synapse/homeserver.yaml` configured for `http://localhost:8008` with open registration
- `docker-compose.override.yml` that exposes ports on `127.0.0.1` and disables coturn/sygnal/web
- tenant stack metadata such as `TENANT_STACK_NAME`, local port variables, and the control-plane tenant config path

### 2. Add Firebase credentials (optional — only needed for push notifications)

```bash
cp sygnal/firebase-service-account.json.example sygnal/firebase-service-account.json
# Sygnal is disabled in local mode, so this step can be skipped for basic dev
```

### 3. Start the stack

```bash
docker compose up -d
```

### 4. Services

| Service | URL |
|---|---|
| Synapse (Matrix API) | http://localhost:8008 |
| Control plane (workspace discovery) | http://localhost:8085 |
| Well-known | http://localhost:8080 |
| Vault API | http://localhost:8090 |
| MinIO console | http://localhost:9001 |

### 5. Connect the Flutter app

In the LetsYak app login screen, enter the homeserver:
```
http://localhost:8008
```

Registration is open in local mode — you can create accounts directly from the app, or via script:

```bash
./scripts/create-user.sh alice 'password123'
./scripts/create-user.sh admin 'password123' --admin
```

The local control plane serves workspace discovery from `control-plane/config/tenants.sample.json`:

```bash
curl 'http://localhost:8085/api/v1/workspaces/resolve?slug=local'
curl 'http://localhost:8085/api/v1/workspaces/resolve?email=alice@example.com'
```

The response includes the workspace display name, Matrix homeserver URL, Vault API URL, branding, isolation tier, and security mode. It deliberately does not expose Synapse admin tokens or deployment secrets.

### Local multi-tenant smoke test

The Compose stack is parameterized so multiple isolated LetsYak tenant stacks can run on one Docker host. Each stack needs its own working directory or Compose project name, its own `TENANT_STACK_NAME`, and unique localhost ports.

Example tenant env files live in `tenants/`:

| Tenant | Matrix | Vault API | MinIO console | Workspace slug |
|---|---|---|---|---|
| `local-a` | `http://localhost:8008` | `http://localhost:8090` | `http://localhost:9001` | `local-a` |
| `local-b` | `http://localhost:8108` | `http://localhost:8190` | `http://localhost:9101` | `local-b` |

To create the first local tenant from a fresh checkout or copied directory:

```bash
set -a
. ./tenants/local-a.env.example
set +a
./setup.sh --local
docker compose -p letsyak-local-a up -d
```

To create the second local tenant, use a second copy of `letsyak-server/` so generated Synapse config and media files are isolated, then run:

```bash
set -a
. ./tenants/local-b.env.example
set +a
./setup.sh --local
docker compose -p letsyak-local-b up -d
```

For app workspace switching, point the app at one shared control-plane config that contains both tenants. The sample file `control-plane/config/tenants.local-multi.sample.json` resolves both `local-a` and `local-b`.

```bash
curl 'http://localhost:8085/api/v1/workspaces/resolve?slug=local-a'
curl 'http://localhost:8085/api/v1/workspaces/resolve?slug=local-b'
```

This local smoke setup deliberately keeps each tenant's Matrix, Vault API, Postgres, Redis, and MinIO data isolated. The control-plane is the shared discovery layer.

For production-style local testing, run the discovery layer as its own Compose project and run tenant stacks with their bundled control-plane disabled:

```bash
# Shared workspace discovery / tenant router
docker network create letsyak-control-plane-local 2>/dev/null || true
PROXY_NETWORK_NAME=letsyak-control-plane-local \
CONTROL_PLANE_TENANT_CONFIG=./control-plane/config/tenants.local-multi.sample.json \
docker compose -f docker-compose.control-plane.yml -p letsyak-control-plane up -d --build

# Tenant data plane, from each tenant directory after setup.sh has generated config
docker compose \
  -f docker-compose.yml \
  -f docker-compose.override.yml \
  -f docker-compose.tenant-data-plane.yml \
  -p letsyak-local-a \
  up -d --build
```

To run the automated two-tenant smoke test on macOS/Linux:

```bash
./scripts/smoke-two-tenants.sh
```

The smoke test uses high localhost ports by default so it does not collide with the normal local stack. It creates temporary directories, starts one shared control-plane project plus two tenant data-plane projects, checks Synapse/Vault/MinIO/control-plane routing, creates one Matrix user in each tenant, verifies each user's Vault access, and verifies a tenant A Matrix token is rejected by tenant B Vault.

The smoke stacks are removed automatically. To keep them for manual browser testing:

```bash
KEEP_LETSYAK_SMOKE=1 ./scripts/smoke-two-tenants.sh
```

With the default smoke ports, tenant A is available at `http://localhost:18008` and tenant B at `http://localhost:18108`.

The Flutter web smoke test lives in `mayberychat/scripts/smoke-web-two-tenants.sh`. It starts the backend smoke stacks, runs a Chrome test from the browser runtime, and then cleans everything up:

```bash
cd ../mayberychat
./scripts/smoke-web-two-tenants.sh
```

Use `KEEP_LETSYAK_WEB_SMOKE=1` to leave the backend stacks running for manual browser checks.

For real-device testing on the same Wi-Fi network, bind the smoke services to all interfaces and publish your Mac's LAN IP in workspace discovery:

```bash
LAN_IP=$(ipconfig getifaddr en0)
KEEP_LETSYAK_SMOKE=1 \
LETSYAK_SMOKE_BIND_ADDRESS=0.0.0.0 \
LETSYAK_SMOKE_PUBLIC_HOST="$LAN_IP" \
./scripts/smoke-two-tenants.sh
```

Then use these URLs from the device browser or app:

| Tenant | Matrix | Control plane | Vault API |
|---|---|---|---|
| `local-a` | `http://$LAN_IP:18008` | `http://$LAN_IP:18085` | `http://$LAN_IP:18090` |
| `local-b` | `http://$LAN_IP:18108` | `http://$LAN_IP:18085` | `http://$LAN_IP:18190` |

For Flutter web on a real device, run the app with a LAN-visible web server and make `web/config.json` point `controlPlaneBaseUrl` at the LAN control-plane URL before serving the build.

### Resetting local state

```bash
docker compose down -v   # removes all containers AND data volumes
./setup.sh --local       # regenerate config with fresh secrets
docker compose up -d
```

---

## Quick Start (This Server - chat.maybery.app)

### 1. Stop the old Matrix stack

```powershell
cd C:\docker\chat
docker compose --env-file .env.matrix -f docker-compose.matrix.yml down
```

### 2. Run setup

```powershell
cd C:\docker\letsyak-server
.\setup.ps1
```

It prompts for:
| Prompt | Default | Example |
|---|---|---|
| Matrix domain | `chat.maybery.app` | Press Enter |
| TURN domain | `turn.maybery.app` | Press Enter |
| Server public IP | (none) | `102.222.241.2` |

All secrets are auto-generated. Config files are written to `synapse/`, `coturn/`, and `well-known/www/`.

### 3. Start the stack

```powershell
docker compose up -d
```

### 4. Configure NPM

In Nginx Proxy Manager (http://localhost:81), set up **one proxy host** for `chat.maybery.app`:

#### Main settings
| Field | Value |
|---|---|
| Domain Names | `chat.maybery.app` |
| Scheme | `http` |
| Forward Hostname / IP | `letsyak-synapse` |
| Forward Port | `8008` |
| Websockets Support | ON |
| Cache Assets | OFF |
| Block Common Exploits | ON |

#### SSL tab
| Field | Value |
|---|---|
| SSL Certificate | Request new or use existing for `chat.maybery.app` |
| Force SSL | ON |
| HTTP/2 Support | ON |
| HSTS Enabled | ON |

#### Custom Locations tab

Add a custom location for well-known:

| Field | Value |
|---|---|
| Location | `/.well-known` |
| Scheme | `http` |
| Forward Hostname / IP | `letsyak-well-known` |
| Forward Port | `80` |

#### Advanced tab

Paste this nginx config:
```nginx
client_max_body_size 50M;
proxy_http_version 1.1;
proxy_read_timeout 600s;
proxy_send_timeout 600s;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header Host $host;
```

The launch chat-media policy is a single global Synapse upload limit of `50M` via `max_upload_size`. Keep NPM's `client_max_body_size` at the same value or higher so the reverse proxy does not reject valid Synapse uploads before Synapse can return a Matrix error. Larger durable file sharing should go through Vault rather than normal chat uploads.

### 5. Verify DNS

Ensure these records exist in Cloudflare:

| Record | Type | Value | Proxy |
|---|---|---|---|
| `chat.maybery.app` | A | `102.222.241.2` | Proxied (orange cloud) OK |
| `turn.maybery.app` | A | `102.222.241.2` | **DNS only (grey cloud)** |

> TURN/STUN traffic is UDP — Cloudflare doesn't proxy it. `turn.maybery.app` MUST be grey cloud.

### 6. Verify the stack

```powershell
# All containers running?
docker compose ps

# Synapse responding?
docker compose exec synapse curl -sS http://localhost:8008/_matrix/client/versions

# Well-known from outside?
# (run from another machine or use browser)
# https://chat.maybery.app/.well-known/matrix/client
# https://chat.maybery.app/.well-known/matrix/server

# Federation test:
# https://federationtester.matrix.org/api/report?server_name=chat.maybery.app
```

### 7. Create admin user

```powershell
.\scripts\create-user.ps1 -Username admin -Password 'YourStrongPassword!' -Admin
```

### 8. Connect LetsYak / FluffyChat

Set the homeserver URL in your LetsYak app to:
```
https://chat.maybery.app
```

---

## Deploying for a New Client (Cloud VPS)

The same `letsyak-server/` directory works on any Linux VPS. The deployment model
differs slightly since cloud instances typically use Caddy or their own reverse proxy
instead of NPM.

### Option A: Client VPS with its own NPM

1. Install Docker on the VPS
2. Deploy NPM on the VPS (or use any reverse proxy)
3. Copy `letsyak-server/` to the VPS
4. Run `./setup.sh` with the client's domain
5. Configure NPM with the same settings above, using the client's domain
6. `docker compose up -d`

### Option B: Shared server (this machine)

Multiple clients can run on the same server with different subdomains. Each gets
a separate `letsyak-server` directory and compose project:

```
C:\docker\letsyak-acme\        → chat.acme.letsyak.com
C:\docker\letsyak-widgets\     → chat.widgets.letsyak.com
C:\docker\letsyak-server\      → chat.maybery.app (your own)
```

Each tenant needs a unique `TENANT_STACK_NAME`, unique public domains, and isolated generated config/secrets. `docker-compose.yml` prefixes container names and internal Docker networks from `TENANT_STACK_NAME`, while Compose project names keep named volumes isolated.

The preferred shared-host shape is one shared control-plane Compose project plus many tenant data-plane Compose projects.

Recommended shared-host pattern:

```bash
# Shared workspace discovery / tenant router
CONTROL_PLANE_TENANT_CONFIG=./control-plane/config/tenants.production.json \
docker compose -f docker-compose.control-plane.yml -p letsyak-control-plane up -d --build

# Tenant data planes
TENANT_STACK_NAME=letsyak-acme ./setup.sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.tenant-data-plane.yml \
  -p letsyak-acme \
  up -d --build

TENANT_STACK_NAME=letsyak-widgets ./setup.sh
docker compose \
  -f docker-compose.yml \
  -f docker-compose.tenant-data-plane.yml \
  -p letsyak-widgets \
  up -d --build
```

In Nginx Proxy Manager, forward the control-plane hostname to `letsyak-control-plane:8085`. Forward tenant hostnames to the tenant-prefixed container names, for example `letsyak-acme-synapse:8008`, `letsyak-acme-well-known:80`, and `letsyak-acme-vault-api:8090`.

> **Note:** Coturn ports (3478, 5349) can only bind once. Multiple clients sharing
> a server should share a single coturn instance with the same TURN secret
> configured in all their `homeserver.yaml` files.

---

## User Management

```powershell
# Regular user
.\scripts\create-user.ps1 -Username alice -Password 'password123'

# Admin user
.\scripts\create-user.ps1 -Username admin -Password 'password123' -Admin
```

Linux:
```bash
./scripts/create-user.sh alice 'password123'
./scripts/create-user.sh admin 'password123' --admin
```

---

## Common Operations

### View logs
```powershell
docker compose logs -f synapse
docker compose logs -f coturn
docker compose logs -f postgres
docker compose logs -f well-known
```

### Restart a service
```powershell
docker compose restart synapse
```

### Adjust chat media upload limit

Normal chat attachments use Synapse media, not Vault. The launch limit is global for all clients and platforms:

```yaml
max_upload_size: 50M
```

If this changes, update `synapse/homeserver.yaml`, keep the NPM `client_max_body_size` at the same value or higher, and restart Synapse with `docker compose restart synapse`. Web, iOS, and Android clients all inherit the same server-side Matrix upload policy.

### Update images
```powershell
docker compose pull
docker compose up -d
```

### Stop everything
```powershell
docker compose down
```

---

## Backup & Restore

### Backup
```powershell
# 1. Database dump
docker compose exec postgres pg_dump -U synapse synapse > "backup_db_$(Get-Date -Format 'yyyy-MM-dd').sql"

# 2. Synapse config + media + signing key
tar czf "backup_synapse_$(Get-Date -Format 'yyyy-MM-dd').tar.gz" synapse/

# 3. Environment (contains all secrets)
Copy-Item .env "backup_env_$(Get-Date -Format 'yyyy-MM-dd')"
```

### Restore
```powershell
# 1. Restore configs
tar xzf backup_synapse_YYYY-MM-DD.tar.gz
Copy-Item backup_env_YYYY-MM-DD .env

# 2. Start postgres only
docker compose up -d postgres
Start-Sleep -Seconds 5

# 3. Restore database
Get-Content backup_db_YYYY-MM-DD.sql | docker compose exec -T postgres psql -U synapse synapse

# 4. Start everything
docker compose up -d
```

---

## Troubleshooting

### Synapse won't start
```powershell
docker compose logs synapse
# Common: postgres not ready. Restart synapse.
docker compose restart synapse
```

### NPM can't reach Synapse
- Confirm Synapse is on `proxy-network`: `docker network inspect proxy-network`
- Container name must match what you entered in NPM: `letsyak-synapse`
- Compose service name for `exec`/`logs` is `synapse`, not `letsyak-synapse`

### Well-known not working
- Check NPM custom location routes `/.well-known` to `letsyak-well-known:80`
- Test directly: `docker compose exec well-known curl -s http://localhost/.well-known/matrix/client`

### Federation not working
- Test: `https://federationtester.matrix.org/api/report?server_name=chat.maybery.app`
- Ensure port 443 is forwarded through to NPM
- If Cloudflare proxied, SSL mode must be **Full (strict)**

### TURN/calls not working
- Ensure `turn.maybery.app` is **DNS only** (grey cloud) in Cloudflare
- Ensure ports 3478 and 49160-49200/udp are open in firewall
- Check: `docker compose logs coturn`

---

## Future: LiveKit Integration (Phase 2)

When ready for video calling, add to `docker-compose.yml`:

```yaml
  livekit:
    image: livekit/livekit-server:latest
    container_name: letsyak-livekit
    restart: unless-stopped
    ports:
      - "7880:7880"
      - "7881:7881"
      - "7882:7882/udp"
    volumes:
      - ./livekit/config.yaml:/etc/livekit.yaml:ro
    command: ["--config", "/etc/livekit.yaml"]
    networks:
      - letsyak-internal
      - proxy-network
```

Add an NPM proxy host for the LiveKit WebSocket endpoint and configure
MatrixRTC auth. Details in the Phase 2 plan.
