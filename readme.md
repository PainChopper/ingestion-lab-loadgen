# Ingestion Lab

Ingestion Lab (`ingestion-lab-loadgen`) replays Parquet transactions through a rate-controlled Go pipeline with a React control panel.

[![Go CI](https://github.com/PainChopper/ingestion-lab-loadgen/actions/workflows/go-ci.yml/badge.svg?branch=main)](https://github.com/PainChopper/ingestion-lab-loadgen/actions/workflows/go-ci.yml) · [Live demo (simulation)](https://painchopper.github.io/ingestion-lab-loadgen/) · [Receiver](#receiver) · [LoadGen](#loadgen) · [React UI](#react-ui)

![Running Ingestion Lab simulation showing Reader and Sender workers, queue occupancy, throughput, and the Reader panel with its simulation source](docs/ingestion-lab-simulation.png)

## Why Ingestion Lab?

Change the reader and sender worker counts or the transaction rate while the pipeline is running.
Watch queue occupancy and blocked time to see where backpressure starts.
Start with the browser simulation or run both Go services locally to send real HTTP batches.

## Quick start

Clone the repository. With Go 1.27 and Node.js 24 with npm installed, start each component in a separate terminal. Begin each block at the repository root.

### Receiver

```bash
cd receiver
go run .
```

Receiver listens on `127.0.0.1:8081`, accepts batches at `POST /internal/ingest`, and exposes acceptance counters at [`GET /status`](http://127.0.0.1:8081/status).

### LoadGen

```bash
cd loadgen
go run .
```

LoadGen listens on `127.0.0.1:8080` and automatically starts reading the bundled sample and posting JSON batches to Receiver. The default glob, `../data/MBD-mini/trx/fold=*/*.parquet`, is relative to `loadgen/` and also matches files added in the same layout. To use a personal TOML file outside the checkout, run `go run . serve --config /path/to/loadgen.toml` from `loadgen/`.

### React UI

```bash
cd frontend
npm ci
npm run dev:backend
```

Open the URL Vite prints, usually [localhost:5173/ingestion-lab-loadgen/](http://localhost:5173/ingestion-lab-loadgen/). Vite proxies `/api` to LoadGen. To run the browser simulation locally, use `npm run dev` from `frontend/` after the same install. The simulation generates activity in the browser without a dataset or HTTP delivery.

## Local infrastructure

Copy `infra/.env.example` to `infra/.env` and set a strong local `POSTGRES_PASSWORD` before the first start. The file is Git-ignored. From the repository root:

```bash
docker compose --env-file infra/.env -f infra/docker-compose.yml up -d --wait
docker compose --env-file infra/.env -f infra/docker-compose.yml ps
```

| Service or route | Host address | Address on the Compose network |
| --- | --- | --- |
| Kafka direct | `127.0.0.1:9092` | `kafka:29092` |
| Kafka via Toxiproxy | `127.0.0.1:19092` (`kafka-host`) | `toxiproxy:29093` (`kafka-container`) |
| Host Receiver via Toxiproxy | `http://127.0.0.1:18081` | `http://toxiproxy:18081` |
| Toxiproxy API | `http://127.0.0.1:8474` | `http://toxiproxy:8474` |
| Redis | `127.0.0.1:6379` | `redis:6379` |
| PostgreSQL | `127.0.0.1:5432` | `postgres:5432` |
| Prometheus | `http://127.0.0.1:9090` | `http://prometheus:9090` |
| Grafana | `http://127.0.0.1:3000` | `http://grafana:3000` |

Use the Kafka address for your client location and chosen route. The broker advertises the same address it was reached through, so metadata preserves that route. Use direct connections for ordinary runs and select the proxy addresses for network experiments. Toxiproxy starts with no toxics. To test the Receiver path, start Receiver on the host and check `http://127.0.0.1:18081/status`. The proxy forwards to `host.docker.internal:8081`. If the container cannot reach the default loopback bind, set `server.address` in a local Receiver config to a host interface reachable from Docker.

Check that all six services are healthy in `ps`, then inspect the loaded proxies at `http://127.0.0.1:8474/proxies`. PostgreSQL uses the `ingestion_lab` database and user. Stop the stack with `docker compose --env-file infra/.env -f infra/docker-compose.yml down` to keep the named volumes.

With the stack healthy and Receiver running on host port `8081`, run [the smoke check](infra/smoke.sh) from the repository root. It needs Bash, Docker Compose, `curl`, `jq`, `kcat`, `redis-cli`, `psql` and `timeout`. On Windows with Docker Desktop, run it from WSL with Docker integration and the published ports reachable at `127.0.0.1`. On native Linux, bind Receiver to an interface Docker can reach, such as the Docker bridge gateway, or to `0.0.0.0` on an isolated test host.

```bash
bash infra/smoke.sh
```

The script checks Kafka messages and advertised addresses on all four routes and verifies the Receiver proxy. It runs Redis `PING` and PostgreSQL `SELECT 1` from the host and from each service's own container. It checks Redis AOF and fsync settings from the host. Run it while the proxies are idle so its temporary latency on the Receiver route does not affect other clients. The script removes its test topics and latency setting when it exits.

Kafka runs one combined KRaft broker/controller with plaintext connections and no ACLs. Auto-creation is off. Create topics explicitly with replication factor 1. For a three-broker cluster, use replication factor 3 and `min.insync.replicas=2`, and reassign existing topics. Amazon MSK and Azure Event Hubs' Kafka endpoint require their own client security and broker settings.

Dependabot checks Compose images, both Go modules and GitHub Actions monthly. Each ecosystem groups its updates, including major versions, with a limit of three open version-update pull requests. Upgrade PostgreSQL majors manually with a data migration plan.

Pull requests to `main` require `Check loadgen`, `Check receiver` and `Infra smoke`. On a pull request, `Infra smoke` starts a disposable Compose stack when files under `infra/` or `.github/workflows/infra-ci.yml` change. It reports skipped when neither changes. A manual Infra CI run always runs the smoke check. Dependabot can auto-squash a group after the required checks pass when every update is identifiable as minor or patch. Groups with a major or unclassified update require manual review.

## Architecture

```text
Parquet → Reader pool → Reader queue → Throttler → Sender queue → Sender pool → HTTP Receiver
```

Readers emit batches of up to the configured size. The Throttler paces whole batches by transaction count, and the Sender retries failed HTTP deliveries. A `204 No Content` acknowledges a batch.

- The [control package](loadgen/internal/control) serializes run transitions, live commands, and snapshot requests in its event loop.
- Receiver's [configuration package](receiver/internal/config) loads and validates its TOML settings before startup.
- Bounded channels propagate backpressure upstream when the next stage falls behind.
- Monotonic time and cumulative counters let clients calculate rates across polls.
- Strict TOML and control API parsing surface invalid settings and commands.
- Shutdown stops reading and gives pending batches up to ten seconds to drain.

See [architecture decisions](docs/decisions.md) for the tradeoffs.

## Reference

- [Configuration](docs/configuration.md) for TOML settings, delivery, and shutdown.
- [HTTP API](docs/api.md) for snapshots, commands, and CLI examples. Open [Swagger UI](http://127.0.0.1:8080/developer/swagger/ui/) while LoadGen is running.
- [Observability](docs/observability.md) for Prometheus metrics, Go profiling, and the monitoring services in [`infra/`](infra/docker-compose.yml).
- [Testing](docs/testing.md) for Go and frontend checks.

## Code and data

The code is [MIT licensed](LICENSE). The repository includes one original, unmodified MBD-mini Parquet file. The full [MBD-mini dataset on Hugging Face](https://huggingface.co/datasets/ai-lab/MBD-mini) is by sb-ai-lab and licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). See the [data NOTICE](data/MBD-mini/trx/fold=0/NOTICE) and [arXiv:2409.17587](https://arxiv.org/abs/2409.17587).

The project is developed with AI assistance. The author is responsible for engineering decisions, code review, and acceptance.
