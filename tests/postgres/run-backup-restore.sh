#!/usr/bin/env bash
set -euo pipefail

# This test exercises the operator backup contract inside the same PostgreSQL
# image used by run-integration.sh. It intentionally stores the archive in a
# temporary directory and never writes credentials to the repository.
container="afh-postgres-backup-test-$$"
password="afh-backup-test-password"
archive=$(mktemp -t afh-postgres-backup.XXXXXX.dump)

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -f -- "$archive"
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
  -c 'CREATE TABLE backup_probe (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO backup_probe VALUES (1, '\''durable'\'');' >/dev/null
docker exec "$container" pg_dump -U postgres -d afh_test -Fc >"$archive"
test -s "$archive"

docker exec "$container" psql -U postgres -d afh_test -v ON_ERROR_STOP=1 \
  -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
docker exec -i "$container" pg_restore -U postgres -d afh_test --exit-on-error <"$archive"
value=$(docker exec "$container" psql -U postgres -d afh_test -At \
  -c 'SELECT value FROM backup_probe WHERE id=1')
if [[ "$value" != "durable" ]]; then
  printf 'backup restore probe=%q, want durable\n' "$value" >&2
  exit 1
fi
printf 'PostgreSQL backup/restore: pass\n'
