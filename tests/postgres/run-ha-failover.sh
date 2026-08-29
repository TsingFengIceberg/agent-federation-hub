#!/usr/bin/env bash
set -Eeuo pipefail

suffix=$$
network="afh-ha-net-$suffix"
primary="afh-ha-primary-$suffix"
standby="afh-ha-standby-$suffix"
password="afh-ha-password"
replication_password="afh-replication-password"

log() { printf '[postgres-ha] %s\n' "$*"; }
cleanup() {
  docker rm -f "$primary" "$standby" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v docker >/dev/null || { log 'docker is required'; exit 2; }
docker network create "$network" >/dev/null

log 'Starting PostgreSQL primary with WAL replication enabled'
docker run --rm -d --name "$primary" --network "$network" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=afh_test \
  postgres:17-alpine -c wal_level=replica -c max_wal_senders=10 -c max_replication_slots=2 >/dev/null
for _ in $(seq 1 120); do
  if docker exec "$primary" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$primary" pg_isready -U postgres -d afh_test >/dev/null
docker exec "$primary" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 -c \
  "CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '$replication_password'; CREATE TABLE failover_probe (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO failover_probe VALUES (1, 'replicated-before-failover');" >/dev/null
docker exec "$primary" sh -c "printf 'host replication replicator 0.0.0.0/0 scram-sha-256\nhost all all 0.0.0.0/0 scram-sha-256\n' >>\"\$PGDATA/pg_hba.conf\""
docker exec -u postgres "$primary" pg_ctl -D /var/lib/postgresql/data reload >/dev/null

log 'Taking a physical base backup into the standby'
docker run --rm -d --name "$standby" --network "$network" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB=afh_test postgres:17-alpine sleep infinity >/dev/null
docker exec -u postgres "$standby" sh -c 'rm -rf /var/lib/postgresql/data/*'
docker exec -u postgres "$standby" sh -c "PGPASSWORD='$replication_password' pg_basebackup -h '$primary' -D /var/lib/postgresql/data -U replicator -Fp -Xs -P -R"
# The image's initialized data directory is intentionally world-writable while
# the temporary standby is used as a sleep container. PostgreSQL refuses to
# start a data directory with those permissions, so restore its required mode
# before launching the replica.
docker exec -u postgres "$standby" chmod 700 /var/lib/postgresql/data
docker exec -u postgres "$standby" postgres -D /var/lib/postgresql/data -c hot_standby=on >/dev/null 2>&1 &
standby_pid=$!
for _ in $(seq 1 120); do
  if docker exec "$standby" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$standby" pg_isready -U postgres -d afh_test >/dev/null
docker exec "$standby" psql -U postgres -d afh_test -At -c 'SELECT value FROM failover_probe WHERE id=1' | grep -qx replicated-before-failover

log 'Stopping primary and promoting standby'
docker stop "$primary" >/dev/null
docker exec -u postgres "$standby" pg_ctl -D /var/lib/postgresql/data promote >/dev/null
for _ in $(seq 1 120); do
  if docker exec "$standby" psql -U postgres -d afh_test -At -c 'SELECT pg_is_in_recovery()' 2>/dev/null | grep -qx f; then
    break
  fi
  sleep 0.25
done
docker exec "$standby" psql -U postgres -d afh_test -At -c 'SELECT pg_is_in_recovery()' | grep -qx f
docker exec "$standby" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 \
  -c "INSERT INTO failover_probe VALUES (2, 'written-after-promotion')" >/dev/null
docker exec "$standby" psql -U postgres -d afh_test -At -c 'SELECT count(*) FROM failover_probe' | grep -qx 2
kill "$standby_pid" 2>/dev/null || true
log 'pass: physical standby replayed state and accepted writes after promotion'
