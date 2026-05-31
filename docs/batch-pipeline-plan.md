# Batch Pipeline Plan

## Implementation Steps

1. Introduce `TransactionBatch`.
2. Change the producer output from individual transactions to `TransactionBatch` values.
3. Keep one parquet file as one `TransactionBatch`.
4. Add a dedicated consumer goroutine.
5. Move transaction processing from `main` into the consumer.
6. Add transaction-count quantum processing inside the consumer.
7. Publish consumer progress through atomic counters after each quantum.
8. Add cancellation checks after each quantum.
9. Replace the old combined `main` select with a control-plane select.
10. Keep the control-plane select responsible for HTTP commands, quit, status, metrics ticks, and lifecycle/error events.
11. Update actual TPS, total transactions, and Prometheus metrics from atomic counter snapshots on the metrics ticker.
12. Add a shared cancellable context for producer and consumer.
13. On quit, call cancel and enter shutdown flow.
14. Make the producer check context between files.
15. Make the producer close the batch channel when it exits.
16. Make the consumer exit normally after observing cancellation between quanta.
17. Make `main` wait for producer and consumer completion.
18. Add a short graceful shutdown timeout.
19. Apply hard exit policy if graceful shutdown exceeds the timeout.
20. Add bounded read-ahead after the base batch pipeline works.
21. Benchmark read-ahead capacity `2`, `4`, `8`, and `16`.

## Control-Plane Select

The new `main` select is introduced after data-plane processing is moved to the consumer.

It handles:

- HTTP control commands
- quit
- status requests
- metrics ticker
- producer completion
- consumer completion
- producer errors
- consumer errors

## Read-Ahead Follow-Up

Read-ahead should use a bounded queue of loaded `TransactionBatch` values.

Start with capacity `2` or `4`.

Increase only after checking throughput, heap growth, and GC behavior.
