#!/bin/sh
set -eu

image="eggy:smoke"
container="eggy-smoke-$$"
data_dir="$(mktemp -d "${TMPDIR:-/tmp}/eggy-smoke.XXXXXX")"

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$data_dir"
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
  --env DEEPSEEK_API_KEY=fake \
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
docker exec "$container" curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
echo "Eggy Docker smoke test passed"
