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
- Backend snapshots with cumulative counters, monotonic capture time, queue occupancy, and applied settings.
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
go run .
```

The default Receiver configuration listens on `127.0.0.1:8081`, accepts batches at `POST /internal/ingest`, and reports acceptance counters at `GET /status` (also supports HEAD). Configuration is unversioned; section and key names are case-sensitive. Unknown keys and sections, including empty sections, and missing required keys are rejected. Explicitly provide `lab.response_delay_ms`, including zero. Omit `lab.response_status` for 204; an explicit zero is invalid. See [configuration](docs/configuration.md#конфигурация-receiver) for exact paths and lab settings.

Start LoadGen in another terminal.

From the repository root:

```bash
cd loadgen
```

The example `[source].path` is `../data/MBD-mini/trx/fold=*/*.parquet`, resolved from the server working directory (`loadgen/` for the commands below). Set it to your dataset; an absolute glob also works, for example:

```toml
[source]
path = '/home/user/datasets/transactions/fold=*/*.parquet'
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
go run . set tps 2000001 --url http://127.0.0.1:8080
go run . set throttle true --url http://127.0.0.1:8080
```

## Configuration and delivery

See [loadgen/config.toml](loadgen/config.toml) for the current configuration example. Configuration is unversioned. Startup batch size and capacities are scalars; workers use initial/max, TPS uses initial/max and accepts any integer from zero to max inclusive, and installed is a boolean. Section and key names are case-sensitive; unknown keys and sections, including empty sections, and missing fields are rejected at startup. Explicit zero values are supported where valid. Changes to a key's meaning, units or allowed values require a new key name, including for optional keys; the old name is rejected as unknown.

| Setting | Purpose | When it can be changed |
| --- | --- | --- |
| `http.addr` | HTTP server address | At startup |
| `source.path` | Glob pattern for Parquet files, relative to the server working directory or absolute | At startup |
| `reader.read_batch_size` | Transactions per Reader batch | At startup |
| `reader.workers`, `sender.workers` | Worker counts | At runtime |
| `readerChannel.capacity`, `senderChannel.capacity` | Queue capacity in batches | At startup |
| `throttler.requested_tps` | Requested transaction rate | At runtime |
| `throttler.installed` | Enable or disable rate limiting | At runtime |
| `sender.api.url` | HTTP delivery endpoint | At startup |
| `sender.api.timeout_ms` | HTTP timeout per delivery attempt (30,000 ms in the example) | At startup |
| `sender.retry` | Positive, nondecreasing retry delays; jitter from 0% to 50% (example: 20%) | At startup |

A capacity of `0` means an unbuffered Go channel. With the Throttler enabled, `0 TPS` holds batches; disabling the Throttler removes the rate limit. Rate limiting operates on whole batches: the delay is calculated from the batch size and requested TPS.

On Ctrl+C or SIGTERM, LoadGen stops reading new rows and gives the remaining batches up to 10 seconds to reach the Receiver. The rate limit stays in effect, including at zero TPS. At the deadline, LoadGen cancels pending work and logs the confirmed and unconfirmed transaction totals.

The HTTP Sender posts a JSON array with `Content-Type: application/json` and treats `204 No Content` as success. Failed attempts are retried according to the configured delay schedule. Once the final interval is reached, retries continue at that delay until delivery succeeds or is cancelled. This is a controllable ingestion pipeline lab, not a general-purpose browser simulator or a load-testing tool for arbitrary protocols.

## HTTP API and observability

- `GET /api/loadgen/snapshot`: run status and live stage/queue telemetry. The immutable `config` object contains `readerWorkersMax`, `senderWorkersMax`, `requestedTpsMax`, `readBatchSize`, `readerChannelCapacity`, `senderChannelCapacity`, and `sourcePath`.
- `HEAD /api/loadgen/snapshot`: the same response headers without a body.
- `POST /api/loadgen/commands`: change live settings while the pipeline is running.
- `GET /metrics`: Prometheus metrics.
- `/debug/pprof/`: Go profiling.

Snapshots expose cumulative `reader.rowsRead`, channel `inputTransactionsTotal`/`outputTransactionsTotal`, channel `blockedMs`, and confirmed `run.totalTransactions`. They never reset during the process, including retries, pool downscale and shutdown. The root `capturedAtMs` uses the server's monotonic clock from one service startup origin. All observations use one capture time and are read sequentially; brief differences between counters are possible while workers make progress. Clients calculate rates from counter differences and server capture-time differences. The first observation, a repeated capture time, or a decreased counter/time after restart gives no rate for that interval and establishes the next baseline.

Source errors expose `operation`, the exact file `path` (or glob pattern for discovery failures), and `message`. CLI displays the configured source glob and labels queued transactions as an approximation of `depthBatches × config.readBatchSize`; partial batches can contain fewer transactions.

CLI `status` makes one snapshot request and shows explicitly labelled average TPS since the run started (total divided by `run.elapsedMs`), including admitted TPS from the Sender channel's input total. Before the run starts, averages are zero. Prometheus `loadgen_transactions_total` reads current confirmations at every scrape; `loadgen_target_tps` remains the configured target. Runtime summaries have their own one-minute timer and log cumulative channel totals.

Reader and Sender snapshots expose `workerStates`: a list of live workers, each with `activity` and `draining`. Reader activities are `idle`, `reading`, and `blocked`; Sender activities are `idle`, `inFlight`, and `backoff`. A blocked Reader still owns its file, and a Sender in backoff still owns its batch. The independent `draining` flag prevents new work after current work finishes. Lists are empty (`[]`) before startup, allow repeated states, and have no ordering guarantee. `workers` remains the configured target; during downscale it can be smaller than the list length. CLI displays counts derived from these lists; runtime summaries log only the target and live count for each pool.

Example command body (accepted with the example maximum of 4000000):

```json
{"action":"set-requested-tps","value":2000001}
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
