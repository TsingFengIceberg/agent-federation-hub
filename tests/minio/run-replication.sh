#!/usr/bin/env bash
set -Eeuo pipefail

suffix=$$
network="afh-minio-replication-$suffix"
source_name="afh-minio-source-$suffix"
target_name="afh-minio-target-$suffix"
access_key="afhreplaccess"
secret_key="afhreplsecretkey"
mc_image="minio/mc:RELEASE.2025-08-13T08-35-41Z"

log() { printf '[minio-replication] %s\n' "$*"; }
cleanup() {
  docker rm -f "$source_name" "$target_name" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v docker >/dev/null || { log 'docker is required'; exit 2; }
docker network create "$network" >/dev/null
mc() {
  docker run --rm -i --network "$network" \
    -e "MC_HOST_source=http://$access_key:$secret_key@$source_name:9000" \
    -e "MC_HOST_target=http://$access_key:$secret_key@$target_name:9000" \
    "$mc_image" "$@"
}
for name in "$source_name" "$target_name"; do
  docker run --rm -d --name "$name" --network "$network" \
    -e MINIO_ROOT_USER="$access_key" -e MINIO_ROOT_PASSWORD="$secret_key" \
    minio/minio:RELEASE.2025-09-07T16-13-09Z server /data >/dev/null
done
for pair in "source:$source_name" "target:$target_name"; do
  alias_name=${pair%%:*}
  name=${pair##*:}
  for _ in $(seq 1 120); do
    mc ready "$alias_name" >/dev/null 2>&1 && break
    sleep 0.25
  done
done

log 'Configuring versioned buckets and mirroring an Artifact object'
mc mb --ignore-existing source/artifacts >/dev/null
mc mb --ignore-existing target/artifacts >/dev/null
mc version enable source/artifacts >/dev/null
mc version enable target/artifacts >/dev/null
printf 'artifact-replication-probe' | mc pipe source/artifacts/tenant-a/task-1/object-1 >/dev/null
mc mirror --overwrite --remove source/artifacts target/artifacts >/dev/null
value=$(mc cat target/artifacts/tenant-a/task-1/object-1)
[[ "$value" == artifact-replication-probe ]] || { log "replicated object=$value"; exit 1; }
log 'pass: versioned Artifact object mirrored to an independent object-store target'
