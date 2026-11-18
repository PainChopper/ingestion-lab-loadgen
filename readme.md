# Ingestion Lab

A working load generator built in Go with a React control panel. It reads transactions from Parquet files, controls the processing rate, and delivers JSON batches to an HTTP endpoint. The UI displays run status, throughput, queue occupancy, worker settings, and delivery results.

[Live demo](https://painchopper.github.io/ingestion-lab-loadgen/) · [Go backend](loadgen/) · [React frontend](frontend/)

## Current capabilities

The end-to-end pipeline is implemented: data reading, rate limiting, real HTTP delivery, retries, and a backend-connected UI. The HTTP Sender is part of the current implementation, not a future roadmap item.

The public demo uses simulation so you can explore the interface without running the backend or generating network load. To use the real pipeline, run the project locally in backend mode.

```text
Parquet → Reader workers → Reader queue → Throttler → Sender queue → Sender workers → HTTP target
```

- One run per LoadGen process, started automatically and stopped with Ctrl+C or SIGTERM.
- Read Parquet files with configurable batch sizes and Reader/Sender worker counts.
- Two configurable queues and optional batch-level transaction rate limiting (TPS).
- HTTP POST delivery of JSON batches, retries with delays and jitter, and cancellation handling.
- A standalone HTTP receiver for local experiments.
- Backend snapshots with rates, counters, queue occupancy, and applied settings.
- Prometheus metrics and standard Go `pprof` endpoints.
- A React/TypeScript frontend built with Vite.
- Automated Go tests and frontend tests using Vitest and Testing Library.

The project is developed with AI assistance. The author remains responsible for engineering decisions, code review, and acceptance of the final work.

## Running on Linux

Requires Go 1.27 and Node.js 24 with npm. Backend mode requires compatible transaction data in Parquet format; the dataset is not included in the repository.

### Backend

Start Receiver in its own terminal, from the repository root:

```bash
cd receiver
go run ./cmd/receiver --config config.example.toml
```

The example Receiver listens on `127.0.0.1:8081`, accepts batches at `POST /internal/ingest`, and reports acceptance counters at `GET /status`.

Start LoadGen in another terminal.

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

With the included configuration, LoadGen listens on `127.0.0.1:8080` and its Sender posts to `http://127.0.0.1:8081/internal/ingest`. Set `sender.api.url` to use a different target.

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
go run . status --url http://127.0.0.1:8080
go run . set readers 2 --url http://127.0.0.1:8080
go run . set senders 4 --url http://127.0.0.1:8080
go run . set tps 200000 --url http://127.0.0.1:8080
go run . set throttle true --url http://127.0.0.1:8080
```

## Configuration and delivery

See [loadgen/config.toml](loadgen/config.toml) for the current configuration example. Unknown or missing fields are rejected at startup. Explicit zero values are supported where valid.

| Setting | Purpose | When it can be changed |
| --- | --- | --- |
| `http.addr` | HTTP server address | At startup |
| `source.path` | Absolute glob pattern for Parquet files | At startup |
| `reader.read_batch_size` | Transactions per Reader batch | At startup |
| `reader.workers`, `sender.workers` | Worker counts | At runtime |
| `readerChannel.capacity`, `senderChannel.capacity` | Queue capacity in batches | At startup |
| `throttler.requested_tps` | Requested transaction rate | At runtime |
| `throttler.installed` | Enable or disable rate limiting | At runtime |
| `sender.api.url` | HTTP delivery endpoint | At startup |
| `sender.api.timeout_ms` | HTTP timeout per delivery attempt (default 30,000 ms in schema 3) | At startup |
| `sender.retry` | Positive, nondecreasing retry delays; jitter from 0% to 50% (example: 20%) | At startup |

A capacity of `0` means an unbuffered Go channel. With the Throttler enabled, `0 TPS` holds batches; disabling the Throttler removes the rate limit. Rate limiting operates on whole batches: the delay is calculated from the batch size and requested TPS.

On Ctrl+C or SIGTERM, LoadGen stops reading new rows and gives the remaining batches up to 10 seconds to reach the Receiver. The rate limit stays in effect, including at zero TPS. At the deadline, LoadGen cancels pending work and logs the confirmed and unconfirmed transaction totals.

The HTTP Sender posts a JSON array with `Content-Type: application/json` and treats `204 No Content` as success. Failed attempts are retried according to the configured delay schedule. Once the final interval is reached, retries continue at that delay until delivery succeeds or is cancelled. This is a controllable ingestion pipeline lab, not a general-purpose browser simulator or a load-testing tool for arbitrary protocols.

## HTTP API and observability

- `GET /api/loadgen/snapshot`: run status, stage and queue telemetry, and configuration.
- `HEAD /api/loadgen/snapshot`: the same response headers without a body.
- `POST /api/loadgen/commands`: change live settings while the pipeline is running.
- `GET /metrics`: Prometheus metrics.
- `/debug/pprof/`: Go profiling.

Example command body:

```json
{"action":"set-requested-tps","value":200000}
```

Supported actions: `set-reader-workers`, `set-sender-workers`, `set-requested-tps`, and `set-throttler-installed`.

Invalid actions or values receive `400 Bad Request`. During shutdown, snapshots remain available while batches drain and commands receive `409 Conflict`. The backend UI shows batch size and queue capacity as read-only values.

Unsupported methods return `405` with `Allow: GET, HEAD` for snapshots or `Allow: POST` for commands. Command bodies over 4096 bytes return `413` before JSON validation. Interrupted runtime waits return `503`; a command may already have been applied. Command `200` and `409` responses have empty bodies.

## Tests

Backend checks, from the `loadgen/` directory:

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

CLI black-box tests choose an available port and also run delivery while port 8080 is occupied. Startup retries are limited to three attempts and only recognize an address-in-use error from their own subprocess. A separate test holds a listener to verify a real bind conflict before restarting through the ordinary helper.

Frontend checks, from the `frontend/` directory:

```bash
npm test
npm run lint
npm run build
```

The repository also contains a separate [receiver module](receiver/). Historical plans and research in `docs/` may describe earlier stages; this README and the source code describe the current working implementation.
