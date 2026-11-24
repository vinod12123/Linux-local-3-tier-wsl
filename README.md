# AstraPay local three-tier failure lab

A portable Docker Compose application for testing real detection, incident creation, investigation and remediation from an ASTRA host agent installed in Ubuntu WSL.

The business application has exactly three tiers:

1. **Nginx frontend** — serves the payment UI and proxies `/api`.
2. **Go backend API** — validates and processes payments.
3. **PostgreSQL database** — permanently stores the test transactions in a named volume.

A fourth, out-of-band **lab controller** provides failure and recovery controls. It is not part of the payment request path and remains reachable when the frontend fails.

## Run everything

Nothing needs to be installed manually in Ubuntu. The script uses Docker Desktop WSL integration when it is available. Otherwise, on Ubuntu/Debian WSL it installs the packaged Docker Engine and Docker Compose v2, starts the service, and then builds the lab. Ubuntu may ask for the user's `sudo` password during that automatic installation.

```bash
git clone <your-repository-url>
cd Linux-local-3-tier-wsl
bash setup.sh
```

The script creates local configuration, downloads/builds the images, starts every service, waits for health and normally opens the application automatically.

- Payment application: <http://localhost:18080>
- Recovery console: <http://localhost:18082>
- Backend readiness: <http://localhost:18081/health/ready>

No domain or TLS certificate is required for this local lab.

## Real simulation behavior

Create a payment first. It is written into PostgreSQL and appears in the transaction table. Then use the application controls:

| Tier | Failure visible to the user | Evidence produced |
| --- | --- | --- |
| Frontend | The whole website becomes unreachable | Nginx container exit and Docker lifecycle event |
| Backend | Nginx returns `502 Bad Gateway` | API container exit, Nginx error/access logs and Docker lifecycle event |
| Database | Payment and history operations return `503` | PostgreSQL exit, API database connection errors and Docker lifecycle event |

Each tier has three controls:

- **Crash** terminates its process abnormally. This is the appropriate case for testing an agentic restart.
- **Stop** performs an intentional Docker stop. ASTRA can detect and open an incident, but should not automatically reverse an intentional human action.
- **Restore** starts the same container again. Database data remains available because the volume is preserved.

If Nginx is stopped or crashed, open the recovery console directly on port `18082` to restore it.

## ASTRA host-agent checklist

Install/re-enrol the current Linux host agent in the organization being tested and enable:

- host metrics and logs;
- process and Docker/container collection;
- Docker lifecycle events;
- agent actions if real remediation will be tested;
- restart allowlisting only for `astra-lab-frontend`, `astra-lab-backend` and `astra-lab-database`.

The containers carry `com.cloudscore.lab=true` and tier labels to make discovery easier. Generate the Linux installation command from the ASTRA organization that should receive the data; do not reuse a one-time enrollment token.

## Useful commands

```bash
# Show status and recent logs
bash status.sh

# Stop the lab but keep payment data
bash stop.sh

# Start/rebuild it again
bash setup.sh

# Permanently remove the lab and its database volume
bash reset.sh
```

## Configuration

On the first run, `setup.sh` copies `.env.example` to `.env` and generates a random controller token. Ports can be changed in `.env` before starting.

The default `LAB_BIND_ADDRESS=127.0.0.1` restricts the application and controller to the same computer. If access from another trusted computer is genuinely required, change it to `0.0.0.0` and protect the host with an appropriate firewall; never expose the controller to the internet.

## Security boundary

The recovery controller mounts `/var/run/docker.sock`, which grants Docker-engine-level access inside that container. Its HTTP API contains a fixed allowlist and will only address the three exact lab container names. This project is intentionally a local failure lab and must not be deployed to a shared or production server.

No real payment provider is contacted and no real money is charged. The transactions and failures are genuine local application/database operations; the business data is test data.
