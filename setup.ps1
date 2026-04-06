# =============================================================================
# LetsYak Server Setup (PowerShell)
# Generates all configuration and secrets for the Matrix stack.
# Run once before 'docker compose up -d'.
# =============================================================================

$ErrorActionPreference = "Stop"

function New-Secret {
    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RNGCryptoServiceProvider]::new()
    $rng.GetBytes($bytes)
    $rng.Dispose()
    return ([Convert]::ToBase64String($bytes)).Replace('/', '').Replace('+', '')
}

Write-Host ""
Write-Host "+===========================================+" -ForegroundColor Cyan
Write-Host "|        LetsYak Server Setup               |" -ForegroundColor Cyan
Write-Host "+===========================================+" -ForegroundColor Cyan
Write-Host ""

# --- Prerequisites -----------------------------------------------------------
try {
    docker info 2>$null | Out-Null
} catch {
    Write-Host "Error: Docker is not running." -ForegroundColor Red
    exit 1
}

# --- Guard against re-run ----------------------------------------------------
if (Test-Path .env) {
    $confirm = Read-Host "Warning: .env already exists. Overwrite? (y/N)"
    if ($confirm -ne 'y' -and $confirm -ne 'Y') { exit 0 }
}

# --- Gather configuration ----------------------------------------------------
Write-Host "Configuration" -ForegroundColor Cyan
Write-Host "-------------"

$MatrixDomain = Read-Host "Matrix domain [chat.maybery.app]"
if ([string]::IsNullOrWhiteSpace($MatrixDomain)) { $MatrixDomain = "chat.maybery.app" }

$TurnDomain = Read-Host "TURN domain [turn.maybery.app]"
if ([string]::IsNullOrWhiteSpace($TurnDomain)) { $TurnDomain = "turn.maybery.app" }

do { $PublicIP = Read-Host "Server public IP address" }
while ([string]::IsNullOrWhiteSpace($PublicIP))

# --- Generate secrets ---------------------------------------------------------
Write-Host "`nGenerating secrets..." -ForegroundColor Yellow
$PostgresPassword   = New-Secret
$RegistrationSecret = New-Secret
$MacaroonSecret     = New-Secret
$FormSecret         = New-Secret
$TurnSecret         = New-Secret

# --- Create .env --------------------------------------------------------------
$timestamp = Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ"
@"
# LetsYak Server Configuration
# Generated: $timestamp

MATRIX_DOMAIN=$MatrixDomain
TURN_DOMAIN=$TurnDomain
PUBLIC_IP=$PublicIP

POSTGRES_PASSWORD=$PostgresPassword
REGISTRATION_SECRET=$RegistrationSecret
MACAROON_SECRET=$MacaroonSecret
FORM_SECRET=$FormSecret
TURN_SECRET=$TurnSecret
"@ | Set-Content -Path ".env" -Encoding UTF8
Write-Host "  + .env" -ForegroundColor Green

# --- Create directories -------------------------------------------------------
$dirs = @("synapse", "synapse/media_store", "coturn", "scripts", "well-known/www")
foreach ($d in $dirs) { New-Item -ItemType Directory -Force -Path $d | Out-Null }

# --- Generate Synapse signing key ---------------------------------------------
Write-Host "Generating Synapse signing key..." -ForegroundColor Yellow
$synapsePath = (Resolve-Path ./synapse).Path.Replace('\', '/')
docker run --rm `
    -v "${synapsePath}:/data" `
    --entrypoint python3 `
    matrixdotorg/synapse:latest `
    -c "
from signedjson.key import generate_signing_key, write_signing_keys
key = generate_signing_key('auto')
with open('/data/signing.key', 'w') as f:
    write_signing_keys(f, [key])
" 2>$null
Write-Host "  + synapse/signing.key" -ForegroundColor Green

# --- Create homeserver.yaml ---------------------------------------------------
@"
# LetsYak Synapse Configuration
# Domain: $MatrixDomain

server_name: "$MatrixDomain"
public_baseurl: "https://$MatrixDomain/"
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
    password: "$PostgresPassword"
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
enable_registration: false
enable_registration_without_verification: false
allow_public_rooms_without_auth: false
allow_public_rooms_over_federation: false

registration_shared_secret: "$RegistrationSecret"
macaroon_secret_key: "$MacaroonSecret"
form_secret: "$FormSecret"

signing_key_path: "/data/signing.key"
trusted_key_servers:
  - server_name: "matrix.org"

turn_uris:
  - "turn:${TurnDomain}:3478?transport=udp"
  - "turn:${TurnDomain}:3478?transport=tcp"
  - "turns:${TurnDomain}:5349?transport=tcp"
turn_shared_secret: "$TurnSecret"
turn_user_lifetime: 1h
turn_allow_guests: false

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
"@ | Set-Content -Path "synapse/homeserver.yaml" -Encoding UTF8
Write-Host "  + synapse/homeserver.yaml" -ForegroundColor Green

# --- Create log config --------------------------------------------------------
@"
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
"@ | Set-Content -Path "synapse/log.config" -Encoding UTF8
Write-Host "  + synapse/log.config" -ForegroundColor Green

# --- Create coturn config -----------------------------------------------------
@"
# LetsYak TURN Server
use-auth-secret
static-auth-secret=$TurnSecret
realm=$MatrixDomain
server-name=$TurnDomain

listening-port=3478
tls-listening-port=5349
external-ip=$PublicIP

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
"@ | Set-Content -Path "coturn/turnserver.conf" -Encoding UTF8
Write-Host "  + coturn/turnserver.conf" -ForegroundColor Green

# --- Create well-known JSON files ---------------------------------------------
@"
{
  "m.homeserver": {
    "base_url": "https://$MatrixDomain"
  }
}
"@ | Set-Content -Path "well-known/www/client.json" -Encoding UTF8
Write-Host "  + well-known/www/client.json" -ForegroundColor Green

@"
{
  "m.server": "${MatrixDomain}:443"
}
"@ | Set-Content -Path "well-known/www/server.json" -Encoding UTF8
Write-Host "  + well-known/www/server.json" -ForegroundColor Green

# --- Fix ownership for Synapse container (UID 991) ----------------------------
Write-Host "Setting file permissions..." -ForegroundColor Yellow
docker run --rm `
    -v "${synapsePath}:/data" `
    --entrypoint sh `
    matrixdotorg/synapse:latest `
    -c "chown -R 991:991 /data" 2>$null
Write-Host "  + Permissions set" -ForegroundColor Green

# --- Summary ------------------------------------------------------------------
Write-Host ""
Write-Host "+===========================================+" -ForegroundColor Green
Write-Host "|         Setup Complete!                   |" -ForegroundColor Green
Write-Host "+===========================================+" -ForegroundColor Green
Write-Host ""
Write-Host "NEXT STEPS:" -ForegroundColor Cyan
Write-Host ""
Write-Host "1. DNS records (if not already set):" -ForegroundColor Yellow
Write-Host "   $MatrixDomain  ->  A  $PublicIP  (can be Cloudflare proxied)"
Write-Host "   $TurnDomain    ->  A  $PublicIP  (MUST be DNS only / grey cloud)"
Write-Host ""
Write-Host "2. Firewall ports:" -ForegroundColor Yellow
Write-Host "   3478/tcp+udp     TURN"
Write-Host "   5349/tcp+udp     TURNS (TLS)"
Write-Host "   49160-49200/udp  TURN relay range"
Write-Host "   (80/443 already open for NPM)"
Write-Host ""
Write-Host "3. Stop old Matrix stack (if running):" -ForegroundColor Yellow
Write-Host "   cd C:\docker\chat"
Write-Host "   docker compose --env-file .env.matrix -f docker-compose.matrix.yml down"
Write-Host ""
Write-Host "4. Start this stack:" -ForegroundColor Yellow
Write-Host "   docker compose up -d"
Write-Host ""
Write-Host "5. Configure NPM proxy host for ${MatrixDomain}:" -ForegroundColor Yellow
Write-Host "   See README.md for exact NPM settings."
Write-Host ""
Write-Host "6. Configure NPM proxy host for well-known:" -ForegroundColor Yellow
Write-Host "   Custom location /.well-known -> letsyak-well-known:80"
Write-Host ""
Write-Host "7. Create admin user:" -ForegroundColor Yellow
Write-Host "   .\scripts\create-user.ps1 -Username admin -Password 'YOUR_PASSWORD' -Admin"
Write-Host ""
Write-Host "8. Point LetsYak app to:" -ForegroundColor Yellow
Write-Host "   https://${MatrixDomain}"
Write-Host ""
