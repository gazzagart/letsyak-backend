## File Vault Deployment Guide — Docker + NPM + Cloudflare

### Overview

You're adding **2 new subdomains** and **2 new Docker services** to your existing stack:

| Component | Domain | Container | Port |
|---|---|---|---|
| Vault API | `vault-api.maybery.app` | `letsyak-vault-api` | 8090 |
| MinIO (S3 storage) | `vault-files.maybery.app` | `letsyak-minio` | 9000 |

---

### Step 1 — Cloudflare DNS Records

In your Cloudflare dashboard for `maybery.app`:

1. Go to **DNS > Records**
2. Add two new **A records**:

| Type | Name | Content | Proxy status | TTL |
|---|---|---|---|---|
| A | `vault-api` | `102.222.241.2` | **Proxied** (orange cloud) | Auto |
| A | `vault-files` | `102.222.241.2` | **Proxied** (orange cloud) | Auto |

3. Under **SSL/TLS > Overview**, confirm mode is **Full (strict)**
   - This is required because NPM will have its own SSL certificates and Cloudflare needs to trust them
4. Under **SSL/TLS > Edge Certificates**, confirm **Always Use HTTPS** is ON

> Both domains can be Cloudflare-proxied (orange cloud) since they're HTTPS only — unlike `turn.maybery.app` which must be grey cloud for UDP.

---

### Step 2 — Add Vault Secrets to `.env`

SSH into your server and add these lines to your `.env` file in the letsyak-server directory:

```powershell
cd C:\docker\letsyak-server
```

Open `.env` and add (below the existing `TURN_SECRET` line):

```env
# File Vault
VAULT_API_DOMAIN=vault-api.maybery.app
VAULT_FILES_DOMAIN=vault-files.maybery.app
MINIO_ACCESS_KEY=<generate-a-random-key>
MINIO_SECRET_KEY=<generate-a-random-secret>
```

To generate the secrets:

**PowerShell:**
```powershell
# Access key (32 hex chars)
-join ((48..57) + (97..122) | Get-Random -Count 32 | ForEach-Object {[char]$_})

# Secret key (44 base64 chars)
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RNGCryptoServiceProvider]::new().GetBytes($bytes)
[Convert]::ToBase64String($bytes).Replace('/', '').Replace('+', '')
```

**Linux/bash:**
```bash
openssl rand -hex 16          # access key
openssl rand -base64 32 | tr -d '/+'  # secret key
```

---

### Step 3 — Create the Vault Database

Your PostgreSQL volume already exists (it was initialized when you first ran the stack), so the init script in `postgres-initdb/` won't run automatically. Create the database manually:

```powershell
docker compose exec postgres psql -U synapse -c "CREATE DATABASE vault;"
docker compose exec postgres psql -U synapse -c "GRANT ALL PRIVILEGES ON DATABASE vault TO synapse;"
```

> On a **fresh** deployment (new server), this step is automatic — the init script handles it.

---

### Step 4 — Build and Start the New Services

```powershell
cd C:\docker\letsyak-server

# Pull MinIO image and build vault-api from the Dockerfile
docker compose build vault-api
docker compose pull minio

# Start everything (existing services unaffected)
docker compose up -d
```

Verify they're running:

```powershell
docker compose ps
```

You should see `letsyak-minio` and `letsyak-vault-api` both in **running** state with healthy status.

Check the logs for any startup errors:

```powershell
docker compose logs vault-api --tail 20
docker compose logs minio --tail 20
```

The vault-api should log: `LetsYak Vault API listening on :8090`

---

### Step 5 — Configure NPM — Vault API Proxy Host

In Nginx Proxy Manager (`http://localhost:81`), click **Add Proxy Host**:

#### Details tab
| Field | Value |
|---|---|
| Domain Names | `vault-api.maybery.app` |
| Scheme | `http` |
| Forward Hostname / IP | `letsyak-vault-api` |
| Forward Port | `8090` |
| Cache Assets | OFF |
| Block Common Exploits | ON |
| Websockets Support | OFF |

#### SSL tab
| Field | Value |
|---|---|
| SSL Certificate | Request a new SSL Certificate |
| Force SSL | ON |
| HTTP/2 Support | ON |
| HSTS Enabled | ON |
| Email Address | *(your email for Let's Encrypt)* |
| I Agree... | Checked |

#### Advanced tab
Paste:
```nginx
client_max_body_size 10M;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header Host $host;
```

Click **Save**.

---

### Step 6 — Configure NPM — MinIO (Vault Files) Proxy Host

Click **Add Proxy Host** again:

#### Details tab
| Field | Value |
|---|---|
| Domain Names | `vault-files.maybery.app` |
| Scheme | `http` |
| Forward Hostname / IP | `letsyak-minio` |
| Forward Port | `9000` |
| Cache Assets | OFF |
| Block Common Exploits | ON |
| Websockets Support | OFF |

#### SSL tab
Same as Step 5 — request a new certificate, Force SSL, HTTP/2, HSTS.

#### Advanced tab
Paste (note the larger body size — this is where files upload to):
```nginx
client_max_body_size 600M;
proxy_http_version 1.1;
proxy_read_timeout 600s;
proxy_send_timeout 600s;
proxy_buffering off;
proxy_request_buffering off;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header Host $host;
```

> **Why 600M?** The default user quota is 500MB, so a single file could be up to 500MB. `proxy_buffering off` and `proxy_request_buffering off` ensure NPM streams the upload directly to MinIO without buffering the entire file in RAM.

Click **Save**.

---

### Step 7 — Cloudflare Cache Rules (Important)

Cloudflare will try to cache responses from `vault-files.maybery.app` by default. You need to disable this because presigned URLs are unique per request and file content is private.

In Cloudflare:

1. Go to **Rules > Page Rules** (or **Cache Rules** in the new UI)
2. Create a rule:
   - **URL match:** `vault-files.maybery.app/*`
   - **Setting:** Cache Level = **Bypass**
3. Create another rule:
   - **URL match:** `vault-api.maybery.app/*`
   - **Setting:** Cache Level = **Bypass**

Alternatively, in the newer Cloudflare Rules UI:

1. Go to **Rules > Cache Rules**
2. **Create rule**: name it `Bypass Vault Files`
   - When: Hostname equals `vault-files.maybery.app`
   - Then: **Bypass cache**
3. **Create rule**: name it `Bypass Vault API`
   - When: Hostname equals `vault-api.maybery.app`
   - Then: **Bypass cache**

> Without these rules, Cloudflare may cache presigned URL responses or serve stale file listings.

---

### Step 8 — Cloudflare Upload Size Limit

By default, Cloudflare's **free plan** limits uploads to **100MB**. If you're on the free plan:

- Users can upload files up to ~100MB through the vault
- To allow larger files, you'd need a **Pro plan** (500MB) or **Business plan** (unlimited)

If you're on the free tier, update `VaultConfig.maxUploadSizeBytes` in the Flutter app to match:

```dart
static const int maxUploadSizeBytes = 104857600; // 100 MB (Cloudflare free tier limit)
```

---

### Step 9 — Verify Everything

Run these checks in order:

```powershell
# 1. MinIO is healthy
docker compose exec minio curl -sf http://localhost:9000/minio/health/live
# Should return silently (exit code 0)

# 2. Vault API is responding
docker compose exec vault-api wget -qO- http://localhost:8090/api/v1/quota
# Should return {"error":"missing authorization header"} — this means it's running

# 3. Vault API can reach Synapse
docker compose exec vault-api wget -qO- http://synapse:8008/_matrix/client/versions
# Should return JSON with Matrix version info

# 4. NPM is proxying vault-api (from any browser)
# Visit: https://vault-api.maybery.app/api/v1/quota
# Should return: {"error":"missing authorization header"}

# 5. NPM is proxying MinIO (from any browser)
# Visit: https://vault-files.maybery.app/minio/health/live
# Should return empty 200 OK

# 6. Test from LetsYak app
# Open LetsYak → tap Vault in the nav rail → should see "Your vault is empty"
```

---

### Troubleshooting Checklist

| Symptom | Likely cause | Fix |
|---|---|---|
| NPM can't find `letsyak-vault-api` | Container not on `proxy-network` | Check `docker network inspect proxy-network` |
| Vault API returns 502 in browser | vault-api container crashed | `docker compose logs vault-api` |
| Uploads fail with 413 | Cloudflare or NPM body size limit | Check Cloudflare plan limit; check NPM advanced `client_max_body_size` |
| Presigned URLs return "connection refused" | `MINIO_PUBLIC_URL` wrong or NPM not proxying MinIO | Check `.env` has correct `VAULT_FILES_DOMAIN`; check NPM proxy host |
| "vault database does not exist" | DB not created | Run Step 3 manually |
| SSL errors on vault domains | Certificates not issued | Check NPM > SSL Certificates; Cloudflare SSL mode must be Full (strict) |
| Uploads succeed but downloads 403 | Cloudflare caching presigned URLs | Add cache bypass rules (Step 7) |

---

### Summary — What You're Creating

```
Cloudflare (DNS + CDN)
  ├─ vault-api.maybery.app   ─── A record → your IP (proxied, cache bypassed)
  └─ vault-files.maybery.app ─── A record → your IP (proxied, cache bypassed)
         │                              │
         ▼                              ▼
NPM (Nginx Proxy Manager)
  ├─ vault-api.maybery.app   → letsyak-vault-api:8090  (SSL, 10M body)
  └─ vault-files.maybery.app → letsyak-minio:9000      (SSL, 600M body, no buffering)
         │                              │
         ▼                              ▼
Docker (letsyak-internal + proxy-network)
  ├─ vault-api  → auth via Synapse, metadata in PostgreSQL (vault db)
  └─ minio      → S3 object storage (minio_data volume)
```