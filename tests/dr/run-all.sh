#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
log() { printf '[dr] %s\n' "$*"; }
cd "$repo_root"
for script in \
  tests/postgres/run-encrypted-backup.sh \
  tests/postgres/run-ha-failover.sh \
  tests/postgres/run-pitr.sh \
  tests/minio/run-replication.sh; do
  log "running $script"
  "$repo_root/$script"
done
log 'all local HA, encrypted-backup, PITR, and object-replication drills passed'
