#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

info() { printf '\033[1;34m[astra-payment-lab]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[astra-payment-lab] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

install_docker() {
  command -v apt-get >/dev/null 2>&1 || fail "Docker is missing and automatic installation is only supported on Ubuntu/Debian."
  command -v sudo >/dev/null 2>&1 || fail "Docker is missing and sudo is required for automatic installation."
  info "Docker is not available; installing Docker Engine and Compose from the Ubuntu package repository..."
  sudo apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io docker-compose-v2
  sudo systemctl enable --now docker 2>/dev/null || sudo service docker start
  sudo usermod -aG docker "${USER}" || true
  info "Docker was installed. This first run will use sudo; future logins can use Docker directly."
}

command -v docker >/dev/null 2>&1 || install_docker

if ! docker compose version >/dev/null 2>&1; then
  command -v apt-get >/dev/null 2>&1 || fail "Docker Compose v2 is unavailable."
  info "Installing the Docker Compose v2 plugin..."
  sudo apt-get update
  sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y docker-compose-v2
fi

if ! docker info >/dev/null 2>&1; then
  sudo systemctl start docker 2>/dev/null || sudo service docker start 2>/dev/null || true
fi

if docker info >/dev/null 2>&1; then
  DOCKER=(docker)
elif sudo docker info >/dev/null 2>&1; then
  DOCKER=(sudo docker)
else
  fail "Docker is installed but its engine is unavailable. Start Docker Desktop with WSL integration or enable systemd in this Ubuntu WSL distribution."
fi

"${DOCKER[@]}" compose version >/dev/null 2>&1 || fail "Docker Compose v2 is unavailable."

if [[ ! -f .env ]]; then
  cp .env.example .env
  if command -v openssl >/dev/null 2>&1; then
    token="$(openssl rand -hex 24)"
  else
    token="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
  fi
  sed -i "s/^LAB_CONTROL_TOKEN=.*/LAB_CONTROL_TOKEN=${token}/" .env
  info "Created local .env configuration."
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

frontend_port="${LAB_FRONTEND_PORT:-18080}"
api_port="${LAB_API_PORT:-18081}"
control_port="${LAB_CONTROL_PORT:-18082}"
bind_address="${LAB_BIND_ADDRESS:-127.0.0.1}"

info "Validating configuration..."
"${DOCKER[@]}" compose config --quiet

info "Downloading base images and building the application..."
"${DOCKER[@]}" compose pull database
"${DOCKER[@]}" compose build --pull

info "Starting PostgreSQL, payment API, Nginx and the recovery controller..."
"${DOCKER[@]}" compose up -d --remove-orphans

info "Waiting for all application tiers to become healthy..."
deadline=$((SECONDS + 180))
while (( SECONDS < deadline )); do
  frontend_state="$("${DOCKER[@]}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' astra-lab-frontend 2>/dev/null || true)"
  backend_state="$("${DOCKER[@]}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' astra-lab-backend 2>/dev/null || true)"
  database_state="$("${DOCKER[@]}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' astra-lab-database 2>/dev/null || true)"
  controller_state="$("${DOCKER[@]}" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' astra-lab-controller 2>/dev/null || true)"
  if [[ "$frontend_state" == healthy && "$backend_state" == healthy && "$database_state" == healthy && "$controller_state" == healthy ]]; then
    break
  fi
  sleep 3
done

if [[ "${frontend_state:-}" != healthy || "${backend_state:-}" != healthy || "${database_state:-}" != healthy || "${controller_state:-}" != healthy ]]; then
  "${DOCKER[@]}" compose ps
  printf '\nRecent logs:\n' >&2
  "${DOCKER[@]}" compose logs --tail=80 >&2
  fail "The lab did not become healthy within 180 seconds. The containers remain available for inspection."
fi

host_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
info "Application is ready."
printf '\n  Payment application: http://localhost:%s\n' "$frontend_port"
printf '  Recovery console:    http://localhost:%s\n' "$control_port"
printf '  Backend API:         http://localhost:%s/health/ready\n' "$api_port"
if [[ "$bind_address" == "0.0.0.0" && -n "$host_ip" ]]; then
  printf '  WSL/LAN address:     http://%s:%s\n' "$host_ip" "$frontend_port"
fi
printf '\nUse the buttons in the application to cause and restore real failures.\n'
printf 'If the frontend is down, use the recovery console on port %s.\n\n' "$control_port"

if command -v powershell.exe >/dev/null 2>&1 && [[ "${1:-}" != "--no-open" ]]; then
  powershell.exe -NoProfile -Command "Start-Process 'http://localhost:${frontend_port}'" >/dev/null 2>&1 || true
fi

