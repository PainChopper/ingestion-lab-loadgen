# Ingestion Lab

A working load generator built in Go with a React control panel. It reads transactions from Parquet files, controls the processing rate, and delivers JSON batches to an HTTP endpoint. The UI displays run status, throughput, queue occupancy, worker settings, and delivery results.

[Live demo](https://painchopper.github.io/ingestion-lab-loadgen/) · [Go backend](loadgen/) · [React frontend](frontend/)

## Current capabilities

The end-to-end pipeline is implemented: data reading, rate limiting, real HTTP delivery, retries, and a backend-connected UI. The HTTP Sender is part of the current implementation, not a future roadmap item.

The public demo uses simulation so you can explore the interface without running the backend or generating network load. To use the real pipeline, run the project locally in backend mode.

```text
Parquet → Reader workers → Reader queue → Throttler → Sender queue → Sender workers → HTTP target
```

- Start, pause, and reset runs through the UI, HTTP API, or CLI.
- Read Parquet files with configurable batch sizes and Reader/Sender worker counts.
- Two configurable queues and optional batch-level transaction rate limiting (TPS).
- HTTP POST delivery of JSON batches, retries with delays and jitter, and cancellation handling.
- A built-in HTTP receiver for local experiments.
- Backend snapshots with rates, counters, queue occupancy, and applied settings.
- Prometheus metrics and standard Go `pprof` endpoints.
- A React/TypeScript frontend built with Vite, plus Storybook for UI component development.
- Automated Go tests and frontend tests using Vitest and Testing Library.

The project is developed with AI assistance. The author remains responsible for engineering decisions, code review, and acceptance of the final work.

## Running on Linux

Requires Go 1.27 and Node.js 24 with npm. Backend mode requires compatible transaction data in Parquet format; the dataset is not included in the repository.

### Backend

From the repository root:

```bash
cd loadgen
```

Before starting, edit `config.toml` and set `[source].path` to an absolute glob pattern for your dataset, for example:

```toml
[source]
path = '/home/user/datasets/transactions/fold=*/*.parquet'
unit = "glob-pattern"
mutability = "startup-only"
```

Then start the server:

```bash
go run .
```

By default, the server reads `config.toml` from the current directory. To use a different file:

```bash
go run . serve --config /home/user/config/loadgen.toml
```

With the configuration included in the repository, the server listens on `127.0.0.1:8080` and the Sender targets the built-in receiver at `http://127.0.0.1:8080/internal/test/ingest`. The receiver path is derived from `sender.api.url`; if no path is specified, it defaults to `/`. Reserved API, metrics, and diagnostics routes cannot be used as receiver paths.

### Backend-connected UI

In another terminal, from the repository root:

```bash
cd frontend
npm ci
npm run dev:backend
```

Vite proxies `/api` to `http://127.0.0.1:8080`. Open the URL printed by Vite, usually `http://localhost:5173/ingestion-lab-loadgen/`.

### Simulation mode

To explore the UI without a backend or dataset:

```bash
cd frontend
npm ci
npm run dev
```

### CLI controls

From the `loadgen/` directory, with the backend already running:

```bash
go run . snapshot --url http://127.0.0.1:8080
go run . run --url http://127.0.0.1:8080
go run . pause --url http://127.0.0.1:8080
go run . reset --url http://127.0.0.1:8080
go run . set requested-tps 2000 --url http://127.0.0.1:8080
```

## Configuration and delivery

See [loadgen/config.toml](loadgen/config.toml) for the current configuration example. Unknown or missing fields are rejected at startup. Explicit zero values are supported where valid.

| Setting | Purpose | When it can be changed |
| --- | --- | --- |
| `http.addr` | HTTP server address | At startup |
| `source.path` | Absolute glob pattern for Parquet files | At startup |
| `reader.read_batch_size` | Transactions per Reader batch | While idle |
| `reader.workers`, `sender.workers` | Worker counts | At runtime |
| `readerChannel.capacity`, `senderChannel.capacity` | Queue capacity in batches | While idle |
| `throttler.requested_tps` | Requested transaction rate | At runtime |
| `throttler.installed` | Enable or disable rate limiting | At runtime |
| `sender.api.url` | HTTP delivery endpoint | At startup |
| `sender.retry` | Retry delays and jitter | At startup |

A capacity of `0` means an unbuffered Go channel. With the Throttler enabled, `0 TPS` holds batches; disabling the Throttler removes the rate limit. Rate limiting operates on whole batches: the delay is calculated from the batch size and requested TPS. Queue capacity cannot be changed during an active run.

The HTTP Sender posts a JSON array with `Content-Type: application/json` and treats `204 No Content` as success. Failed attempts are retried according to the configured delay schedule. Once the final interval is reached, retries continue at that delay until delivery succeeds or is cancelled. This is a controllable ingestion pipeline lab, not a general-purpose browser simulator or a load-testing tool for arbitrary protocols.

## HTTP API and observability

- `GET /api/loadgen/snapshot`: run status, stage and queue telemetry, and configuration.
- `POST /api/loadgen/commands`: run controls and settings.
- `GET /metrics`: Prometheus metrics.
- `/debug/pprof/`: Go profiling.

Example command body:

```json
{"action":"set-requested-tps","value":2000}
```

Supported actions: `run`, `pause`, `reset`, `set-read-batch-size`, `set-reader-workers`, `set-sender-workers`, `set-reader-channel-capacity`, `set-sender-channel-capacity`, `set-requested-tps`, and `set-throttler-installed`.

Changes to idle-only settings are rejected during an active run. Invalid values receive `400 Bad Request`; state conflicts receive `409 Conflict`. In backend mode, the UI displays actual backend snapshots rather than simulated telemetry.

## Tests and component development

Backend checks, from the `loadgen/` directory:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Frontend checks, from the `frontend/` directory:

```bash
npm test
npm run lint
npm run build
npm run storybook
```

The repository also contains a separate [receiver module](receiver/). Historical plans and research in `docs/` may describe earlier stages; this README and the source code describe the current working implementation.
