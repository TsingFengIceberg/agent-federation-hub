#!/usr/bin/env bash
set -euo pipefail

container="afh-postgres-restart-test-$$"
password="afh-restart-test-password"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "$container" \
  -e POSTGRES_PASSWORD="$password" \
  -e POSTGRES_DB=afh_test \
  postgres:17-alpine >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null

docker exec "$container" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 \
  -c 'CREATE TABLE restart_probe (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO restart_probe VALUES (1, '\''before-restart'\'');' >/dev/null
docker restart "$container" >/dev/null
for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null
value=$(docker exec "$container" psql -U postgres -d afh_test -At \
  -c 'SELECT value FROM restart_probe WHERE id=1')
if [[ "$value" != "before-restart" ]]; then
  printf 'PostgreSQL restart recovery probe=%q, want before-restart\n' "$value" >&2
  exit 1
fi
printf 'PostgreSQL restart recovery: pass\n'
