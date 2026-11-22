# Architecture decisions

## Event loop

A single event loop owns the run lifecycle and applies control commands in order while Reader and Sender workers do the data-plane work concurrently. This gives lifecycle changes and control responses one owner. Letting HTTP handlers mutate shared run state directly would require them to coordinate transitions and shutdown across handlers.

## Channel backpressure

Fixed-capacity Go channels carry batches between stages. A full channel makes its producer wait, and a zero-capacity channel hands each batch directly to a consumer. An unbounded queue could keep the Reader busy during a target slowdown while letting the backlog grow indefinitely.

## Monotonic time

LoadGen uses time elapsed from a monotonic process origin for snapshot timestamps and monotonic clock readings for interval timing. A wall-clock adjustment can move calendar time backward or forward, which would distort rates computed from that clock. The elapsed-time origin restarts with the process, so clients reset their baseline when they detect a restart.

## Cumulative counters

Snapshots publish cumulative work and wait times. Clients use differences between observations to compute interval rates. Publishing a server-side rolling rate would fix a sampling window for every consumer and hide how much work occurred between the samples each client actually received.

## Strict parsing

LoadGen validates TOML before serving and checks command fields and types before applying a change. The CLI also validates config and snapshot responses. Silently ignoring misspelled fields or coercing types could make an experiment run with settings other than the operator's intended values.

## Graceful shutdown

Normal shutdown stops new reads, keeps the rate limit in effect, and gives pending batches up to ten seconds to drain. Immediate cancellation could discard batches that are ready to finish. Waiting indefinitely could strand the process if the target keeps failing or the rate is set to zero.
