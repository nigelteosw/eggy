#!/bin/sh
set -eu

image="eggy:smoke"
container="eggy-smoke-$$"
data_dir="$(mktemp -d "${TMPDIR:-/tmp}/eggy-smoke.XXXXXX")"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  # The container runs as root, so the home it writes is root-owned and an
  # unprivileged runner cannot unlink it. A failed cleanup must not mask the
  # smoke result, so the temporary directory is left behind instead.
  rm -rf "$data_dir" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

docker build --tag "$image" .
docker run --detach --name "$container" \
  --env EGGY_FAKE_ADAPTERS=1 \
  --env EGGY_CONFIG=/data/config.yaml \
  --env PORT=8080 \
  --env EGGY_TELEGRAM_OWNER_ID=42 \
  --env EGGY_PUBLIC_BASE_URL=https://eggy-smoke.example \
  --env TELEGRAM_BOT_TOKEN=fake \
  --env TELEGRAM_WEBHOOK_SECRET=fake-webhook \
  --env DEEPSEEK_API_KEY=smoke-provider-secret \
  --volume "$data_dir:/data" \
  "$image" >/dev/null

attempt=0
until docker exec "$container" curl --fail --silent http://127.0.0.1:8080/readyz >/dev/null; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then
    docker logs "$container"
    exit 1
  fi
  sleep 1
done

docker exec "$container" test -s /data/config.yaml
docker exec "$container" sh -c 'test "$(stat -c %a /data/config.yaml)" = 600'
docker exec "$container" sh -c 'grep -Eq "^data_dir:[[:space:]]*/data$" /data/config.yaml'
for context_file in SOUL.md memories/USER.md memories/MEMORY.md; do
  docker exec "$container" test -s "/data/$context_file"
  docker exec "$container" sh -c "test \"\$(stat -c %a /data/$context_file)\" = 600"
done
docker exec "$container" sh -c '! grep -R -F "smoke-provider-secret" /data/config.yaml /data/SOUL.md /data/memories'
docker exec "$container" curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
echo "Eggy Docker smoke test passed"
