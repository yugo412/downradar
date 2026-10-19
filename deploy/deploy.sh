#!/usr/bin/env bash
#
# Builds the api and the web images here, pushes them, and hands off to
# ship.sh, which is the script CI runs too.
#
# Nothing is built on the server: it has 1.9 GB of RAM and already serves other
# sites, so a Nuxt build there would risk the OOM killer taking one of them down.
#
# Needs a `docker login ghcr.io` for the account that owns the packages, unless
# they are public and only ever read.
#
# Environment:
#   REGISTRY        image namespace, default ghcr.io/yugo412
#   DEPLOY_APP      app name, default downradar
#   NUXT_PAGE_CACHE_SECONDS  page cache baked into the bundle, default 60

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

REGISTRY="${REGISTRY:-ghcr.io/yugo412}"
APP="${DEPLOY_APP:-downradar}"
TS="$(date +%Y%m%d%H%M%S)"

API_IMAGE="$REGISTRY/$APP-api:$TS"
WEB_IMAGE="$REGISTRY/$APP-web:$TS"

echo "==> building $API_IMAGE"
docker build -t "$API_IMAGE" backend

echo "==> building $WEB_IMAGE"
docker build \
  --build-arg "NUXT_PAGE_CACHE_SECONDS=${NUXT_PAGE_CACHE_SECONDS:-60}" \
  -t "$WEB_IMAGE" frontend

# The old ship.sh refused to upload a release whose bundle contained the
# internal token, and the check is still worth making: it catches a
# nuxt.config.ts that started reading a secret at build time.
if [ -n "${INTERNAL_TOKEN:-}" ]; then
  echo "==> checking that no secret was baked into the web image"
  if docker run --rm -e INTERNAL_TOKEN --entrypoint sh "$WEB_IMAGE" \
       -c 'grep -rqF "$INTERNAL_TOKEN" /app'; then
    echo "    ERROR: the internal token is inside the bundle, refusing to ship" >&2
    exit 1
  fi
  echo "    no secret found"
fi

echo "==> pushing"
docker push "$API_IMAGE"
docker push "$WEB_IMAGE"

API_IMAGE="$API_IMAGE" WEB_IMAGE="$WEB_IMAGE" ./deploy/ship.sh
