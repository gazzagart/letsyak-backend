param(
    [Parameter(Mandatory=$true)][string]$Username,
    [Parameter(Mandatory=$true)][string]$Password,
    [switch]$Admin
)

$adminFlag = if ($Admin) { '-a' } else { '' }
docker compose exec synapse register_new_matrix_user `
    -c /data/homeserver.yaml `
    http://localhost:8008 `
    -u $Username -p $Password $adminFlag
