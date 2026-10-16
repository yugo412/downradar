#!/usr/bin/env bash
#
# Builds downradar here and ships it to the server.
#
# Nothing is built on the server: it has 1.9 GB of RAM and is already serving
# other production sites, so a Nuxt build there would risk the OOM killer taking
# one of them down.
#
# The shipping itself lives in ship.sh, which the CI workflow also calls.

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

APP="${DEPLOY_APP:-downradar.info}"
TS="$(date +%Y%m%d%H%M%S)"
STAGE="$(mktemp -d)"
WEB_STOPPED=0

cleanup() {
  if [ "$WEB_STOPPED" = "1" ]; then
    docker compose -f compose.dev.yml start web >/dev/null 2>&1 || true
  fi
  rm -rf "$STAGE"
}
trap cleanup EXIT

echo "==> building the api and the seed command for linux/amd64"
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o "$STAGE/downradar-api" ./cmd/server
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o "$STAGE/downradar-seed" ./cmd/seed
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o "$STAGE/downradar-user" ./cmd/user
)

echo "==> building the web bundle"
docker compose -f compose.dev.yml stop web >/dev/null
WEB_STOPPED=1
docker compose -f compose.dev.yml run --rm --no-deps \
  -e "NUXT_API_BASE=http://127.0.0.1:8081" \
  -e "NUXT_PUBLIC_SITE_URL=https://$APP" \
  -e "NUXT_PAGE_CACHE_SECONDS=60" \
  -e "NODE_ENV=production" \
  web npx nuxt build >/dev/null
docker compose -f compose.dev.yml start web >/dev/null
WEB_STOPPED=0

echo "==> staging release $TS"
mkdir -p "$STAGE/web"
cp -r frontend/.output/. "$STAGE/web/"
cp services.yaml "$STAGE/services.yaml"
mkdir -p "$STAGE/seed"
cp seed/vendors.yaml "$STAGE/seed/vendors.yaml"
du -sh "$STAGE" | awk '{print "    size: " $1}'

RELEASE_NAME="$TS" ./deploy/ship.sh "$STAGE"
