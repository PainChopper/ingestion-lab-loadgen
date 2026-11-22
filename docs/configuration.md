# Configuration

## LoadGen

From `loadgen/`, run `go run .` to load [`config.toml`](../loadgen/config.toml) from the working directory. Use `go run . serve --config /path/to/loadgen.toml` for another file. One run starts automatically per process. [The configuration package](../loadgen/internal/config) loads and validates the TOML file before the HTTP server starts. Environment overrides, key-value overrides, and hot reload are unavailable. The [HTTP API](api.md) exposes live worker counts and rate controls.

The TOML format is unversioned. All LoadGen keys shown in the example are required. Names are case-sensitive, and validation rejects unknown keys and sections, including empty tables. Explicit zero and false values are preserved where valid. Integer fields reject strings, booleans, fractions, NaN, and overflow, including elements of `sender.retry.delays_ms`. A change to the meaning, units, or allowed values of a key requires a new name. The old name then fails as unknown, even for optional keys.

### Startup settings

| Key | Meaning and bundled value |
| --- | --- |
| `http.addr` | LoadGen HTTP address. Use a nonempty host and numeric port from 1 to 65535, such as `127.0.0.1:8080` or `[::1]:8080`. Validation needs no scheme, path, or DNS lookup. |
| `source.path` | Parquet glob, absolute or relative to the LoadGen working directory. The default is `../data/MBD-mini/trx/fold=*/*.parquet`. The location of the TOML file does not affect glob resolution. |
| `reader.read_batch_size` | Maximum transactions per batch, positive integer. Default `1000`. The final batch in each file may be smaller. |
| `readerChannel.capacity`, `senderChannel.capacity` | Nonnegative queue capacities in batches. Defaults are `2` and `0`. Zero is an unbuffered handoff. |
| `sender.api.url` | Absolute HTTP(S) URL with a host and no fragment. Default `http://127.0.0.1:8081/internal/ingest`. |
| `sender.api.timeout_ms` | Timeout for each delivery attempt, including response reading. Range 1 to 9,223,372,036,854 ms. Default `30000`. |
| `sender.retry.delays_ms` | Nonempty array of positive, nondecreasing delays in milliseconds. Default `[250, 500, 1000, 2000, 5000]`. The final interval repeats until success or cancellation. |
| `sender.retry.jitter_percent` | Jitter from 0 to 50 percent. Default `20`. Zero disables jitter. Retry durations must fit even with maximum positive jitter. |
| `logging.level` | `debug`, `info`, `warn`, or `error`. Default `info`. |

Batch size must fit the rate calculation (at most 9,223,372,036 on a 64-bit platform). Queue capacities must fit the channel allocation and cannot exceed 2^53−1. Validation checks representability. It does not guarantee sufficient RAM or impose a limit on capacity multiplied by batch size. If you change the LoadGen address, pass the new URL to the CLI and update the Vite proxy for the connected UI.

### Live settings

| Key | Startup and runtime behavior |
| --- | --- |
| `reader.workers`, `sender.workers` | Each requires `initial` and `max`. Valid counts are integers from 1 through `max`. Defaults are 1 of 7 readers and 32 of 32 senders. `sender.workers.max` also sets the HTTP transport's total idle, per-host idle, and per-host connection limits. |
| `throttler.requested_tps` | Requires `initial` and a positive `max`. The initial setting and commands accept any integer from 0 through `max`. Defaults are 2,000,000 of 4,000,000 transactions per second. |
| `throttler.installed` | Boolean, initially `true`. With throttling enabled, zero TPS holds batches. Disabling it bypasses the limit and retains the requested rate. |

Numeric settings exposed in JSON use the safe integer ceiling of 2^53−1 where applicable. [RFC 8259, section 6](https://www.rfc-editor.org/rfc/rfc8259#section-6) explains this interoperability limit. The service defines the minimums, units, and mutability. TOML has no `min`, `step`, `unit`, `mutability`, or `allowed` overrides.

The UI sets its TPS dial step to the smallest value of the form 1, 2, or 5 times a power of ten that is at least 1 and yields at most 11 intervals. The dial includes the exact minimum and maximum. It drops the last intermediate position if it lies less than half a step below the maximum. For example, the step is 500,000 at a maximum of 4,000,000 and 50,000 at a maximum of 250,000. Manual input and API commands may use any in-range integer, such as 2,000,001.

### Delivery and shutdown

Sender uses HTTP POST with `Content-Type: application/json` and a JSON array of transactions. Only `204 No Content` confirms delivery. Retryable work stays with its sender while it waits for the next attempt. Rate limiting paces whole batches based on their actual row count.

On Ctrl+C or SIGTERM, LoadGen stops reading new rows and gives queued and in-flight work up to ten seconds to drain. The configured rate still applies during the drain, including a pause at zero TPS. At the deadline, LoadGen cancels pending work and logs confirmed and unconfirmed transaction totals. Stop LoadGen first so Receiver can accept the remaining batches.

## Receiver

From `receiver/`, run `go run .` to read the bundled [`config.toml`](../receiver/config.toml), or `go run . --config /path/to/receiver.toml` for a different file. Receiver uses the same case-sensitive, unversioned TOML policy. All example keys are required except `lab.response_status`. Unknown keys and sections, including empty tables, are rejected. Strings, booleans, and fractional values are invalid for integer fields.

| Key | Meaning and bundled value |
| --- | --- |
| `server.address` | Listen address. Default `127.0.0.1:8081`. |
| `server.shutdown_timeout_ms` | Positive time allowed for active requests to finish on shutdown. Default `2000`. If the deadline expires, remaining connections close and the process reports an error. |
| `ingest.path` | Exact batch endpoint. Default `/internal/ingest`. |
| `ingest.max_body_bytes` | Maximum request body size. Default `33554432` bytes. |
| `logging.level` | `debug`, `info`, `warn`, or `error` for lifecycle and errors. Receiver does not log each accepted batch. |
| `lab.response_delay_ms` | Delay before the lab response. Set explicitly, including the default `0`. |
| `lab.response_status` | `204`, `400`, or `500`. Omission selects `204`. An explicit zero is invalid. |

Both millisecond duration fields are limited to 9,223,372,036,854. The ingest path must match `^(/[A-Za-z0-9._~-]+)+$`, excluding segments `.` and `..` and the reserved `/status`. Examples include `/internal/ingest` and `/v1/batches`. Receiver accepts batches only on the configured path. See the [Receiver API](api.md#receiver) for request validation and counters.
