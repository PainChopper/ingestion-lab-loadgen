# HTTP API and CLI

LoadGen serves its control API at `http://127.0.0.1:8080` with the bundled configuration. With LoadGen running, open [Swagger UI](http://127.0.0.1:8080/developer/swagger/ui/) to try the API or fetch [the OpenAPI JSON](http://127.0.0.1:8080/developer/openapi.json). The [checked-in specification](../loadgen/openapi/loadgen.openapi.json) is also available offline.

[The API package](../loadgen/internal/api) defines the JSON types shared by the HTTP server and CLI.

## LoadGen endpoints

| Method and path | Response |
| --- | --- |
| `GET /api/loadgen/config` | The immutable startup settings exposed to clients. |
| `GET /api/loadgen/snapshot` | Run status, stage and queue telemetry, and applied live settings. |
| `HEAD /api/loadgen/config`, `HEAD /api/loadgen/snapshot` | The corresponding GET headers without a body. |
| `POST /api/loadgen/commands` | Apply a live setting during a run. |

Config always contains these seven fields: `readerWorkersMax`, `senderWorkersMax`, `requestedTpsMax`, `readBatchSize`, `readerChannelCapacity`, `senderChannelCapacity`, and `sourcePath`. The last field preserves the configured glob. Config remains available whenever the HTTP listener serves requests, including startup, shutdown, and faults, because it does not need the event loop.

The snapshot contains live settings at `reader.workers`, `sender.workers`, `throttler.requestedTps`, and `throttler.installed`. It has no `config` object. Queue depth is in batches. The CLI labels `depthBatches × readBatchSize` as an approximation of queued transactions because a partial batch can contain fewer rows.

`reader.sourceError` is either `null` or an object with `operation`, `path`, and `message`. The path is the exact file path passed to the Reader, or the configured glob for discovery failures. See [Observability](observability.md) for the corresponding logs.

`reader.workerStates` and `sender.workerStates` list live workers with `activity` and `draining`. Reader activities are `idle`, `reading`, and `blocked`. Sender activities are `idle`, `inFlight`, and `backoff`. A blocked Reader still owns its file, and a Sender in backoff still owns its batch. A worker marked `draining` exits after its current work unless the pool is scaled back up and clears that flag. Before the pools start, lists are empty arrays. List order is unspecified and entries may repeat. The `workers` field is the configured target, so a downscale can temporarily leave more live workers than the target.

Snapshots report cumulative `reader.rowsRead`, queue `inputTransactionsTotal` and `outputTransactionsTotal`, queue `blockedMs`, and confirmed `run.totalTransactions`. These values remain cumulative through retries, downscale, and shutdown. `capturedAtMs` is elapsed monotonic time in milliseconds from service startup. One capture time applies to the entire snapshot. Counters are sampled sequentially and may briefly differ while workers advance. Compute transactions per second as 1,000 times the counter change divided by the capture-time change in milliseconds. On the first sample, a repeated capture time, or a decrease in time or the relevant counter, use the current sample as the next baseline and omit the interval rate.

### Live commands

Send one JSON object with an `action` and a typed `value`. For example, this value is within the bundled TPS maximum of 4,000,000.

```json
{"action":"set-requested-tps","value":2000001}
```

| Action | Value |
| --- | --- |
| `set-reader-workers` | Integer from 1 to `readerWorkersMax`. |
| `set-sender-workers` | Integer from 1 to `senderWorkersMax`. |
| `set-requested-tps` | Integer from 0 to `requestedTpsMax`. |
| `set-throttler-installed` | JSON boolean. |

Success returns an empty `200 OK`. An invalid command, value, or JSON body returns `400 Bad Request`. Unknown fields and trailing JSON are rejected. A body over 4096 bytes returns `413` before JSON validation. If the run is not accepting commands, including during the shutdown drain, the endpoint returns an empty `409 Conflict`. Snapshots remain readable during the drain. If the event loop is unavailable or the request's wait is interrupted, snapshots and commands return `503 Service Unavailable`. A command may already have been applied when the wait is interrupted. Check a new snapshot before retrying. Unsupported methods return `405` with `Allow: GET, HEAD` for config and snapshots or `Allow: POST` for commands.

### React UI connection behavior

The backend-connected UI loads config at startup and polls snapshots every second. On a failed poll it retains the most recent telemetry and config, shows a connection error, and disables new commands. The first successful poll after a failure or a decrease in `capturedAtMs` starts a fresh config load. Config from the old connection is discarded before the new telemetry appears, including when a recovered snapshot has a larger timestamp. Old config responses are ignored, and a failed config request is retried during later snapshot polls.

Until current config is available, the connection remains `connecting` or `error`, numeric controls are disabled, batch size and capacities are unknown (`null`), and commands are unavailable. A command already sent has its own response, timeout, and cancellation when the UI connection is disposed. A command still waiting to be sent is checked again against the current connection, run state, and bounds. The UI combines config with live telemetry for display. Batch size and capacities remain read-only. Simulation gets its settings from the browser model, with `simulation` as the displayed source.

## CLI

Run from `loadgen/` while the server is running. The default `--url` is `http://127.0.0.1:8080`. A custom URL must use HTTP or HTTPS with no user info, query, fragment, or base path.

```bash
go run . status --url http://127.0.0.1:8080
go run . set readers 2 --url http://127.0.0.1:8080
go run . set senders 4 --url http://127.0.0.1:8080
go run . set tps 2000001 --url http://127.0.0.1:8080
go run . set throttle true --url http://127.0.0.1:8080
```

`status` fetches config and snapshot separately and rejects unknown fields or trailing JSON in either response. A failed request identifies its endpoint. Reported TPS values are labelled averages since the run started, computed from cumulative totals and `run.elapsedMs`. Before startup they are zero. Admitted TPS uses the Sender queue's input total. Worker activity counts are derived from the live worker lists. `set` confirms a successful command with a snapshot GET only. The applicable field must be present, including explicit zero or false. A failed confirmation of an applied value exits with code 4.

## Receiver

With the [bundled configuration](configuration.md#receiver), Receiver listens at `http://127.0.0.1:8081` and accepts `POST /internal/ingest`. The request must use `Content-Type: application/json` and a nonempty array of transactions. `[{}]` is accepted. Null transactions and empty arrays are rejected. With valid content type, an oversized body returns `413` before JSON validation. Other body or content-type errors return `400`. The configured lab delay precedes the response. Only a `204` increments the accepted batch and transaction counters. A cancelled wait or an error response leaves them unchanged.

`GET /status` returns `startedAt`, `acceptedBatchesTotal`, `acceptedTransactionsTotal`, `lastAcceptedAt`, and `lab` with `responseDelayMs` and `responseStatus`. `HEAD /status` returns the GET headers without a body. Unsupported methods return `405` with `Allow: GET, HEAD` for status or `Allow: POST` for the ingest route.
