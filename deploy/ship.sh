#!/usr/bin/env bash
#
# Ships an already built release to the server.
#
# Called by deploy/deploy.sh locally and by the deploy workflow in CI, so both
# paths do exactly the same thing.
#
# Usage: ship.sh <stage-dir>
#
# Environment:
#   DEPLOY_HOST     ssh target, default ubuntu@43.133.137.16
#   DEPLOY_APP      app name, default downradar.info
#   RELEASE_NAME    release directory name, default a timestamp
#   KEEP_RELEASES   how many releases to keep, default 3

set -euo pipefail

STAGE="${1:-}"
if [ -z "$STAGE" ] || [ ! -d "$STAGE" ]; then
  echo "usage: ship.sh <stage-dir>" >&2
  exit 1
fi

HOST="${DEPLOY_HOST:-ubuntu@43.133.137.16}"
APP="${DEPLOY_APP:-downradar.info}"
ROOT="/var/www/$APP"
KEEP_RELEASES="${KEEP_RELEASES:-3}"
RELEASE="${RELEASE_NAME:-$(date +%Y%m%d%H%M%S)}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=10"

echo "==> checking that no secret was baked into the bundle"
leaked=0
for secret in "${INTERNAL_TOKEN:-}" "${GITHUB_TOKEN:-}"; do
  if [ -n "$secret" ] && grep -rqF "$secret" "$STAGE" 2>/dev/null; then
    echo "    ERROR: a secret was inlined into the bundle, refusing to deploy"
    leaked=1
  fi
done
if [ "$leaked" != "0" ]; then
  exit 1
fi
echo "    no secret found"

echo "==> uploading $RELEASE"
rsync -az --rsync-path="sudo rsync" --chown=downradar:downradar -e "$SSH" \
  "$STAGE/" "$HOST:$ROOT/releases/$RELEASE/"

echo "==> switching current and restarting"
# The login shell on the server is fish, so remote scripts are piped to bash.
# shellcheck disable=SC2029
$SSH "$HOST" "bash -s" <<REMOTE
set -e
sudo -n chmod +x "$ROOT/releases/$RELEASE/downradar-api"
sudo -n ln -sfn "releases/$RELEASE" "$ROOT/current"
sudo -n systemctl enable downradar-api downradar-web >/dev/null 2>&1
sudo -n systemctl restart downradar-api downradar-web
REMOTE

echo "==> waiting for the api"
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

echo "==> deployed $RELEASE"
