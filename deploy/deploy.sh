#!/usr/bin/env bash
#
# Builds downradar on this machine and ships it to the server.
#
# Nothing is built on the server: the Go binary is cross compiled, and the Nuxt
# bundle is built here. The server only has 1.9 GB of RAM and is already
# serving production traffic, so a build there would risk the OOM killer taking
# down one of the other sites.
#
# Releases are immutable directories, and `current` is a symlink, so a rollback
# is just pointing the symlink at the previous release and restarting.

set -euo pipefail

HOST="${DEPLOY_HOST:-ubuntu@43.133.137.16}"
APP="${DEPLOY_APP:-downradar.info}"
ROOT="/var/www/$APP"
KEEP_RELEASES="${KEEP_RELEASES:-3}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=10"

cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

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

echo "==> building the api for linux/amd64"
(
  cd backend
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o "$STAGE/downradar-api" ./cmd/server
)

echo "==> building the web bundle"
docker compose -f compose.dev.yml stop web >/dev/null
WEB_STOPPED=1
docker compose -f compose.dev.yml run --rm --no-deps \
  -e "NUXT_API_BASE=http://127.0.0.1:8081" \
  -e "NUXT_PUBLIC_SITE_URL=https://$APP" \
  -e "GITHUB_REPO=${GITHUB_REPO:-}" \
  -e "NUXT_PAGE_CACHE_SECONDS=60" \
  -e "NODE_ENV=production" \
  web npx nuxt build >/dev/null
docker compose -f compose.dev.yml start web >/dev/null
WEB_STOPPED=0

echo "==> staging release $TS"
mkdir -p "$STAGE/web"
cp -r frontend/.output/. "$STAGE/web/"
cp services.yaml "$STAGE/services.yaml"
du -sh "$STAGE" | awk '{print "    size: " $1}'

echo "==> checking that no secret was baked into the bundle"
# Nuxt inlines process.env reads into the bundle at build time. Reading a secret
# in nuxt.config.ts would ship the value from this machine to production, which
# is both a leak and a functional break: the API would reject the wrong token and
# every visitor would end up sharing one rate limit bucket.
leaked=0
for secret in "${INTERNAL_TOKEN:-}" "${GITHUB_TOKEN:-}"; do
  if [ -n "$secret" ] && grep -rqF "$secret" "$STAGE/web" 2>/dev/null; then
    echo "    ERROR: a secret was inlined into the bundle, refusing to deploy"
    leaked=1
  fi
done
[ "$leaked" = "0" ] || exit 1
echo "    no secret found in the bundle"

echo "==> uploading to $ROOT/releases/$TS"
rsync -az --rsync-path="sudo rsync" --chown=downradar:downradar -e "$SSH" \
  "$STAGE/" "$HOST:$ROOT/releases/$TS/"

echo "==> switching current and restarting"
# The login shell on the server is fish, so remote scripts are piped to bash
# explicitly rather than relying on the default shell.
# shellcheck disable=SC2029
$SSH "$HOST" "bash -s" <<REMOTE
set -e
sudo -n chmod +x "$ROOT/releases/$TS/downradar-api"
sudo -n ln -sfn "releases/$TS" "$ROOT/current"
sudo -n systemctl enable downradar-api downradar-web >/dev/null 2>&1
sudo -n systemctl restart downradar-api downradar-web
REMOTE

echo "==> waiting for the api to answer"
for i in $(seq 1 30); do
  if $SSH "$HOST" "bash -s" <<'REMOTE' 2>/dev/null
curl -sf -o /dev/null http://127.0.0.1:8081/healthz
REMOTE
  then
    echo "    api is up"
    break
  fi
  if [ "$i" = "30" ]; then
    echo "    api did not come up"
    exit 1
  fi
  sleep 2
done

echo "==> pruning old releases, keeping $KEEP_RELEASES"
# shellcheck disable=SC2029
$SSH "$HOST" "bash -s" <<REMOTE
cd "$ROOT/releases"
ls -1dt */ | tail -n +$((KEEP_RELEASES + 1)) | xargs -r sudo -n rm -rf
REMOTE

echo "==> deployed $TS"
