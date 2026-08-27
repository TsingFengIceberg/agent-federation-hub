#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
go_bin=${GO_BIN:-go}
container="afh-minio-test-$$"
access_key="afhtestaccess"
secret_key="afhtestsecretkey"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
  --name "$container" \
  -e MINIO_ROOT_USER="$access_key" \
  -e MINIO_ROOT_PASSWORD="$secret_key" \
  -p 127.0.0.1::9000 \
  minio/minio:RELEASE.2025-09-07T16-13-09Z server /data >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$container" curl -fsS http://127.0.0.1:9000/minio/health/ready >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
docker exec "$container" curl -fsS http://127.0.0.1:9000/minio/health/ready >/dev/null

endpoint=$(docker port "$container" 9000/tcp)
export AFH_TEST_S3_ENDPOINT="$endpoint"
export AFH_TEST_S3_BUCKET="afh-artifact-test"
export AFH_TEST_S3_ACCESS_KEY="$access_key"
export AFH_TEST_S3_SECRET_KEY="$secret_key"
export AFH_TEST_S3_SECURE="false"

cd "$repo_root"
"$go_bin" test -count=1 ./internal/artifact -run TestS3CompatibleObjectStore
