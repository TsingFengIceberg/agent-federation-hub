#!/usr/bin/env bash
set -Eeuo pipefail

suffix=$$
primary="afh-pitr-primary-$suffix"
restore="afh-pitr-restore-$suffix"
work_dir=$(mktemp -d -t afh-pitr.XXXXXX)
wal_dir="$work_dir/wal"
base_dir="$work_dir/base"
password="afh-pitr-password"
target_time=""

log() { printf '[postgres-pitr] %s\n' "$*"; }
cleanup() {
  docker rm -f "$primary" "$restore" >/dev/null 2>&1 || true
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

command -v docker >/dev/null || { log 'docker is required'; exit 2; }
mkdir -p "$wal_dir" "$base_dir"
chmod 777 "$wal_dir" "$base_dir"
log 'Starting PostgreSQL with WAL archiving'
docker run --rm -d --name "$primary" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=afh_test \
  -v "$wal_dir:/wal" postgres:17-alpine \
  -c wal_level=replica -c archive_mode=on \
  -c "archive_command=test ! -f /wal/%f && cp %p /wal/%f" >/dev/null
for _ in $(seq 1 120); do
  if docker exec "$primary" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$primary" pg_isready -U postgres -d afh_test >/dev/null
docker exec "$primary" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 -c \
  "CREATE TABLE pitr_probe (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO pitr_probe VALUES (1, 'before-target');" >/dev/null
docker exec -u postgres "$primary" pg_basebackup -D /tmp/pitr-base -Fp -X stream >/dev/null
docker cp "$primary:/tmp/pitr-base/." "$base_dir/"
target_time=$(date -u -d '+1 second' '+%Y-%m-%d %H:%M:%S+00')
sleep 2
docker exec "$primary" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 -c \
  "INSERT INTO pitr_probe VALUES (2, 'after-target'); SELECT pg_switch_wal();" >/dev/null
sleep 2
docker stop "$primary" >/dev/null

log "Restoring to recovery target $target_time"
docker run --rm -d --name "$restore" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=afh_test \
  -v "$base_dir:/base:ro" -v "$wal_dir:/wal:ro" postgres:17-alpine sleep infinity >/dev/null
docker exec "$restore" sh -c 'rm -rf /var/lib/postgresql/data/* && cp -a /base/. /var/lib/postgresql/data/ && touch /var/lib/postgresql/data/recovery.signal && printf "restore_command=\x27cp /wal/%%f %%p\x27\nrecovery_target_time=\x27%s\x27\n" "$1" > /var/lib/postgresql/data/postgresql.auto.conf' sh "$target_time"
docker exec "$restore" chown -R postgres:postgres /var/lib/postgresql/data
docker exec "$restore" chmod 700 /var/lib/postgresql/data
docker exec -u postgres "$restore" postgres -D /var/lib/postgresql/data >/dev/null 2>&1 &
restore_pid=$!
for _ in $(seq 1 120); do
  if docker exec "$restore" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$restore" pg_isready -U postgres -d afh_test >/dev/null
count=$(docker exec "$restore" psql -U postgres -d afh_test -At -c 'SELECT count(*) FROM pitr_probe')
[[ "$count" == 1 ]] || { log "PITR retained $count rows; expected one"; exit 1; }
kill "$restore_pid" 2>/dev/null || true
log 'pass: base backup plus archived WAL recovered to a pre-change point in time'
