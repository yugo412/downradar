#!/usr/bin/env bash
#
# Deploys an already published pair of images to the host.
#
# Called by deploy/deploy.sh locally and by the deploy workflow in CI, so both
# paths do exactly the same thing.
#
# The host runs Caddy and the Docker daemon. The compose file is copied from the
# repository on every deploy, while the .env beside it is hand managed and never
# overwritten: it carries the secrets and the addresses of that install, the way
# shared/.env used to.
#
# Usage: ship.sh
#
# Environment:
#   DEPLOY_HOST     ssh target, default ubuntu@43.134.10.112
#   DEPLOY_ROOT     directory holding compose.yml and .env, default /srv/downradar
#   API_IMAGE       image to run for the API, required
#   WEB_IMAGE       image to run for the web server, required
#   DEPLOY_ENV_FILE local file to seed DEPLOY_ROOT/.env from when it is missing,
#                   default .env.production
#   IMAGE_KEEP_HOURS how long an unused image is kept for a rollback, default 168

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

HOST="${DEPLOY_HOST:-ubuntu@43.134.10.112}"
ROOT="${DEPLOY_ROOT:-/srv/downradar}"
ENV_FILE="${DEPLOY_ENV_FILE:-.env.production}"
IMAGE_KEEP_HOURS="${IMAGE_KEEP_HOURS:-168}"
SSH="ssh -o BatchMode=yes -o ConnectTimeout=10"

if [ -z "${API_IMAGE:-}" ] || [ -z "${WEB_IMAGE:-}" ]; then
  echo "API_IMAGE and WEB_IMAGE must both be set" >&2
  exit 1
fi

echo "==> preparing $ROOT on $HOST"
# shellcheck disable=SC2029
$SSH "$HOST" "sudo -n mkdir -p '$ROOT' '$ROOT/seed'"

echo "==> uploading the compose file and the vendor registry"
rsync -az --rsync-path="sudo rsync" -e "$SSH" \
  compose.prod.yml "$HOST:$ROOT/compose.yml"
rsync -az --rsync-path="sudo rsync" -e "$SSH" \
  services.yaml "$HOST:$ROOT/services.yaml"
rsync -az --rsync-path="sudo rsync" -e "$SSH" \
  seed/vendors.yaml "$HOST:$ROOT/seed/vendors.yaml"

echo "==> ensuring $ROOT/.env is in place"
if $SSH "$HOST" "sudo -n test -f '$ROOT/.env'"; then
  echo "    it is already there and is left alone"
elif [ -f "$ENV_FILE" ]; then
  echo "    seeding it from $ENV_FILE"
  rsync -az --rsync-path="sudo rsync" -e "$SSH" \
    "$ENV_FILE" "$HOST:$ROOT/.env"
else
  echo "    no .env on $HOST and no $ENV_FILE here to seed it from" >&2
  exit 1
fi

echo "==> running $API_IMAGE and $WEB_IMAGE"
# --wait holds until every service with a healthcheck reports healthy, and fails
# the command if one does not, which is the whole of the readiness check.
# shellcheck disable=SC2029
$SSH "$HOST" "API_IMAGE='$API_IMAGE' WEB_IMAGE='$WEB_IMAGE' bash -s" <<REMOTE
set -e
cd "$ROOT"

# A package built by a workflow is private by default, so the host has to log in
# before it can pull. The workflow hands this job's token over for that, which is
# enough for this pull and expires with the job.
if [ -n "${GHCR_TOKEN:-}" ]; then
  printf '%s' "$GHCR_TOKEN" | sudo -n docker login ghcr.io -u "$GHCR_USER" --password-stdin >/dev/null
fi

sudo -n docker compose pull
if ! sudo -n docker compose up -d --remove-orphans --wait --wait-timeout 180; then
  echo "the stack did not come up, last lines of the api log:" >&2
  sudo -n docker compose logs --tail 40 api >&2 || true
  exit 1
fi
REMOTE

echo "==> waiting for the web server"
for i in $(seq 1 30); do
  if $SSH "$HOST" "curl -sf -o /dev/null http://127.0.0.1:\${WEB_PORT:-3001}/" 2>/dev/null; then
    echo "    web is answering"
    break
  fi
  if [ "$i" = "30" ]; then
    echo "    web did not answer" >&2
    exit 1
  fi
  sleep 2
done

echo "==> pruning images older than ${IMAGE_KEEP_HOURS}h that nothing is using"
# The running images are in use and are never taken by this.
# shellcheck disable=SC2029
$SSH "$HOST" "sudo -n docker image prune -af --filter 'until=${IMAGE_KEEP_HOURS}h' >/dev/null"

echo "==> deployed $API_IMAGE and $WEB_IMAGE"
