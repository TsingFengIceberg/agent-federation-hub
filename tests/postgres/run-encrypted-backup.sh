#!/usr/bin/env bash
set -Eeuo pipefail

container="afh-postgres-encrypted-backup-$$"
password="afh-encrypted-backup-password"
work_dir=$(mktemp -d -t afh-pg-encrypted.XXXXXX)
archive="$work_dir/backup.dump"
ciphertext="$work_dir/backup.dump.enc"
key_file="$work_dir/backup.key"

log() { printf '[encrypted-backup] %s\n' "$*"; }
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

command -v docker >/dev/null || { log 'docker is required'; exit 2; }
command -v openssl >/dev/null || { log 'openssl is required'; exit 2; }

log 'Starting isolated PostgreSQL instance'
docker run --rm -d --name "$container" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=afh_test postgres:17-alpine >/dev/null
for _ in $(seq 1 120); do
  if docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null

docker exec "$container" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 \
  -c 'CREATE TABLE encrypted_backup_probe (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO encrypted_backup_probe VALUES (1, '\''encrypted-durable'\'');' >/dev/null
openssl rand -hex 32 >"$key_file"
docker exec "$container" pg_dump -U postgres -d afh_test -Fc >"$archive"
openssl enc -aes-256-cbc -pbkdf2 -salt -in "$archive" -out "$ciphertext" -pass "file:$key_file" >/dev/null 2>&1
[[ -s "$ciphertext" ]] || { log 'encrypted archive is empty'; exit 1; }
if cmp -s "$archive" "$ciphertext"; then
  log 'encrypted archive unexpectedly matches plaintext'; exit 1
fi

log 'Dropping source data and restoring from encrypted archive'
docker exec "$container" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 \
  -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
openssl enc -d -aes-256-cbc -pbkdf2 -in "$ciphertext" -out "$work_dir/restored.dump" -pass "file:$key_file" >/dev/null 2>&1
docker exec -i "$container" pg_restore -U postgres -d afh_test --exit-on-error <"$work_dir/restored.dump"
value=$(docker exec "$container" psql -U postgres -d afh_test -At -c 'SELECT value FROM encrypted_backup_probe WHERE id=1')
[[ "$value" == encrypted-durable ]] || { log "restore probe=$value"; exit 1; }
log 'pass: encrypted backup, decrypt, restore, and integrity probe'
