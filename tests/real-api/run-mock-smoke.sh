#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
python_project="$repo_root/tests/interop/python-agent"
python_bin="$python_project/.venv/bin/python"
run_dir=$(mktemp -d -t agent-federation-hub-provider-mock.XXXXXX)
env_file="$run_dir/mock.env"
model_config_file="$run_dir/mock-model-config.yaml"
provider_pid=""

cleanup() {
  if [[ -n "$provider_pid" ]]; then
    kill "$provider_pid" 2>/dev/null || true
    wait "$provider_pid" 2>/dev/null || true
  fi
  rm -rf -- "$run_dir"
}
trap cleanup EXIT

cd "$repo_root"
uv sync --locked --project "$python_project"
printf 'MODEL_API_KEY=mock-secret\n' >"$env_file"
printf '%s\n' \
  'model_api:' \
  '  protocol: openai-responses' \
  '  base_url: http://127.0.0.1:4199/v1' \
  '  responses_path: /responses' \
  '  model: mock-model' \
  '  api_key_env: MODEL_API_KEY' \
  '  headers: {}' \
  >"$model_config_file"

"$python_bin" tests/real-api/mock_provider.py \
  >"$run_dir/mock-provider.log" 2>&1 &
provider_pid=$!

attempts=0
until curl --fail --silent http://127.0.0.1:4199/health >/dev/null; do
  attempts=$((attempts + 1))
  if [[ $attempts -ge 100 ]]; then
    echo "mock provider did not start; log follows" >&2
    sed -n '1,120p' "$run_dir/mock-provider.log" >&2
    exit 1
  fi
  sleep 0.1
done

AFH_ENV_FILE="$env_file" AFH_MODEL_CONFIG_FILE="$model_config_file" \
  "$repo_root/tests/real-api/run-smoke.sh"
