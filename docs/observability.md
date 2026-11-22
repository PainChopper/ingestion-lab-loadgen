# Observability

## Metrics and profiling

| Endpoint | What to look for |
| --- | --- |
| [LoadGen `/metrics`](http://127.0.0.1:8080/metrics) | Prometheus exposition. `loadgen_transactions_total` reads the current count of confirmed transactions on every scrape. `loadgen_target_tps` reports the configured target. |
| [LoadGen `/debug/pprof/`](http://127.0.0.1:8080/debug/pprof/) | Go profiling, including goroutine, heap, CPU, and trace endpoints. |
| [Receiver `/status`](http://127.0.0.1:8081/status) | Accepted batches and transactions at the local target. |

The React UI and CLI read the [control API](api.md). `reader_source_failed` logs the failing path as `path`. The `runtime_summary` log runs once a minute, includes cumulative queue totals and configured queue capacities, and reports the configured worker target and live worker count for each pool. It uses `source_error_path` for a source failure in the summary.

## Prometheus and Grafana

Set up `infra/.env` as described in [Local infrastructure](../readme.md#local-infrastructure). From the repository root, start the monitoring services:

```bash
docker compose --env-file infra/.env -f infra/docker-compose.yml up -d --wait prometheus grafana
```

The stack runs Prometheus at [localhost:9090](http://localhost:9090/) and Grafana at [localhost:3000](http://localhost:3000/), with a provisioned Prometheus data source. Start the Go services separately using the [quick start](../readme.md#quick-start). The bundled [scrape config](../infra/prometheus.yml) reads `/metrics` from `host.docker.internal:8080` every two seconds. If that address does not reach LoadGen from the container on your host, use a container-reachable scrape target and set `http.addr` to listen on the corresponding host interface in your [local LoadGen config](configuration.md#loadgen).

Stop the monitoring services with `docker compose --env-file infra/.env -f infra/docker-compose.yml stop prometheus grafana`.
