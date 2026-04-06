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
  └── Well-known (nginx)           ── proxy-network
```

## Prerequisites

- Docker Engine + Docker Compose v2
- Nginx Proxy Manager running on the `proxy-network` Docker network
- Domain name with DNS control (Cloudflare)
- Ports 3478, 5349, 49160-49200/udp open in firewall (80/443 already handled by NPM)

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
client_max_body_size 100M;
proxy_http_version 1.1;
proxy_read_timeout 600s;
proxy_send_timeout 600s;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header Host $host;
```

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

Each needs unique container names and a separate postgres volume. The existing
`docker-compose.yml` uses fixed container names — duplicate the directory and
edit the container names (or parameterize via `COMPOSE_PROJECT_NAME` in `.env`).

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
docker compose restart letsyak-synapse
```

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
