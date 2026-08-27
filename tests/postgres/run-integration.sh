#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
container="afh-postgres-test-$$"
password="afh-test-password"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "$container" \
  -e POSTGRES_PASSWORD="$password" \
  -e POSTGRES_DB=afh_test \
  -p 127.0.0.1::5432 \
  postgres:17-alpine >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$container" pg_isready -U postgres -d afh_test >/dev/null

endpoint=$(docker port "$container" 5432/tcp)
port=${endpoint##*:}
export AFH_TEST_POSTGRES_DSN="postgres://postgres:${password}@127.0.0.1:${port}/afh_test?sslmode=disable"

cd "$repo_root"
"$go_bin" test -count=1 ./internal/core -run TestPostgresTransactionalStoreAndMultiInstanceLeases
"$go_bin" test -count=1 ./internal/access -run TestPostgresRateLimiterCoordinatesAcrossPools
