# Testing

## Go

Run these commands in each Go module (`loadgen/` and `receiver/`):

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

[Go CI](../.github/workflows/go-ci.yml) runs vet, the race-enabled test suite, and golangci-lint in both modules. It runs on pushes to `main` and can also be triggered manually.

LoadGen tests cover Parquet reading, backpressure, live worker and rate changes, retry, shutdown, and the HTTP/CLI contract. The CLI tests exercise a running server, including delivery while the default LoadGen port is occupied and reporting a bind conflict. Receiver tests exercise request validation, response options, and acceptance counters.

## Frontend

From `frontend/` with Node.js 24 and npm:

```bash
npm ci
npm test
npm run lint
npm run build
```

The Vitest and Testing Library suite covers the UI and its simulation and backend connections. The [GitHub Pages workflow](../.github/workflows/deploy-pages.yml) also runs `npm run format:check` before deploying the browser simulation.

## End-to-end smoke test

Follow the [quick start](../readme.md#quick-start) with the bundled data and default settings. Confirm that Receiver's [`/status`](http://127.0.0.1:8081/status) shows increasing `acceptedBatchesTotal` and `acceptedTransactionsTotal` and that the connected UI shows live telemetry.
