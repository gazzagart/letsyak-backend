#!/usr/bin/env bash
set -e

# Create the vault database and grant the synapse user full access.
# This script runs automatically on first PostgreSQL container startup
# (when the data directory is empty). It does NOT run on subsequent starts.

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE DATABASE vault
        ENCODING 'UTF8'
        LC_COLLATE = 'C'
        LC_CTYPE = 'C'
        TEMPLATE template0;
    GRANT ALL PRIVILEGES ON DATABASE vault TO $POSTGRES_USER;
EOSQL
