# Batch Pipeline Decisions

## Context

The load generator is moving from per-transaction delivery to file-sized transaction batches.

The current dataset contains many relatively small parquet files. Reading one file at a time and emitting one transaction per channel send creates avoidable data-plane overhead and visible throughput unevenness between files.

## Accepted Architecture

1. `TransactionBatch` is the unit passed from producer to consumer.
2. For the current implementation, one parquet file maps to one `TransactionBatch`.
3. The producer emits `TransactionBatch` values through a batch channel.
4. Transaction processing runs in a dedicated consumer goroutine.
5. `main` is the control plane.
6. `main` does not consume transaction or batch data.
7. The consumer processes each batch sequentially.
8. Large batches are handled inside the consumer through transaction-count quanta.
9. After each quantum, the consumer publishes progress through atomic counters.
10. After each quantum, the consumer checks cancellation.
11. `main` snapshots or swaps atomic counters on the metrics ticker.
12. Prometheus metrics are updated from the control plane, not per transaction.
13. Producer and consumer share a cancellable context.
14. Normal quit starts graceful cancellation.
15. The producer owns closing the batch channel.
16. The consumer exits after observing cancellation between quanta.
17. `main` waits for producer and consumer completion.
18. If graceful shutdown exceeds a short timeout, hard exit policy applies.
19. Fatal errors may use fast shutdown.
20. Read-ahead is a separate bounded optimization after the base batch pipeline is in place.

## Target Shape

Parquet files are read into file-sized `TransactionBatch` values. The producer sends those batches through a bounded channel. The consumer receives batches and processes transactions in quanta. Progress is published through atomic counters. The main goroutine handles commands, status, metrics, shutdown, and lifecycle events.

## Shutdown Policy

Normal quit uses graceful cancellation first.

Fatal errors may stop the process quickly.

If graceful shutdown does not finish within a short timeout, the process may exit hard.
