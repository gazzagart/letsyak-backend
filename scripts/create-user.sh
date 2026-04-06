#!/usr/bin/env bash
set -euo pipefail
# Usage: ./scripts/create-user.sh <username> <password> [--admin]

USERNAME="${1:?Usage: $0 <username> <password> [--admin]}"
PASSWORD="${2:?Usage: $0 <username> <password> [--admin]}"
ADMIN_FLAG=""

if [ "${3:-}" = "--admin" ]; then
    ADMIN_FLAG="-a"
fi

docker compose exec synapse register_new_matrix_user \
    -c /data/homeserver.yaml \
    http://localhost:8008 \
    -u "$USERNAME" \
    -p "$PASSWORD" \
    $ADMIN_FLAG
