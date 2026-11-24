#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

info() { printf '\033[1;34m[astra-payment-lab]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[astra-payment-lab] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || fail "Docker is not installed."

if docker info >/dev/null 2>&1; then
  DOCKER=(docker)
elif sudo docker info >/dev/null 2>&1; then
  DOCKER=(sudo docker)
else
  fail "The Docker engine is unavailable, so the lab cannot be stopped through Docker."
fi

info "Stopping and removing only the AstraPay lab containers..."

if "${DOCKER[@]}" compose version >/dev/null 2>&1 && [[ -f compose.yaml ]]; then
  "${DOCKER[@]}" compose -f compose.yaml down --remove-orphans
else
  # Exact-name fallback: never target unrelated containers.
  for container in astra-lab-frontend astra-lab-backend astra-lab-database astra-lab-controller; do
    if "${DOCKER[@]}" inspect "$container" >/dev/null 2>&1; then
      "${DOCKER[@]}" stop --time 10 "$container"
    fi
  done
fi

printf '\nAstraPay is stopped. The PostgreSQL volume astra-payment-lab-data was preserved.\n'
printf 'Run bash setup.sh whenever you want to rebuild and start it again.\n'

