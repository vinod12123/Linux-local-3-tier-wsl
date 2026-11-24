#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
if docker info >/dev/null 2>&1; then DOCKER=(docker); else DOCKER=(sudo docker); fi

if [[ "${1:-}" != "--yes" ]]; then
  printf 'This removes all lab containers and permanently deletes the lab PostgreSQL data.\n'
  read -r -p "Type RESET to continue: " answer
  [[ "$answer" == "RESET" ]] || { printf 'Cancelled.\n'; exit 0; }
fi

"${DOCKER[@]}" compose down --volumes --remove-orphans
printf 'Payment lab and its PostgreSQL volume were removed. Run bash setup.sh to create a clean lab.\n'

