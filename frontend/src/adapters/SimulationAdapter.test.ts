import { afterEach, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from './SimulationAdapter'

describe('SimulationAdapter', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('publishes an immutable Sender aggregate snapshot with retained controls', () => {
    const adapter = new SimulationAdapter()
    const snapshot = adapter.getSnapshot()

    expect(Object.isFrozen(snapshot)).toBe(true)
    expect(Object.isFrozen(snapshot.sender)).toBe(true)
    expect(snapshot.sender.workers.applied).not.toBeNull()
    expect(snapshot.sender).toMatchObject({
      liveWorkers: 0,
      idleWorkers: 0,
      inFlightWorkers: 0,
      backoffWorkers: 0,
      drainingWorkers: 0,
    })
    expect(Object.keys(snapshot.sender)).toEqual(expect.arrayContaining([
      'workers',
      'liveWorkers',
      'idleWorkers',
      'inFlightWorkers',
      'backoffWorkers',
      'drainingWorkers',
      'timeoutMs',
    ]))
    adapter.dispose()
  })

  it('continues to dispatch the retained Sender workers control', async () => {
    const adapter = new SimulationAdapter()

    await expect(adapter.dispatch({ type: 'set-sender-workers', value: 3 }))
      .resolves.toMatchObject({ accepted: true, applyMode: 'immediate' })
    expect(adapter.getSnapshot().sender.workers.applied).toBe(3)
    adapter.dispose()
  })

  it('needs a near-maximum Sender pool to sustain maximum TPS and drain demo queues', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    try {
      const initial = adapter.getSnapshot()
      expect(initial.reader.workers.applied).toBe(5)
      expect(initial.reader.readBatchSize.applied).toBe(1_000)
      expect(initial.sender.workers.applied).toBe(3)
      expect(initial.throttler.requestedTps.max).toBe(250_000)
      expect(initial.sender.workers.max).toBe(32)
      await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
      await adapter.dispatch({ type: 'run' })

      for (const workers of [3, 24, 32]) {
        await adapter.dispatch({ type: 'set-sender-workers', value: workers })
        await vi.advanceTimersByTimeAsync(10_000)
        const before = adapter.getSnapshot()
        await vi.advanceTimersByTimeAsync(30_000)
        const after = adapter.getSnapshot()
        const deliveredTps =
          (after.sender.successfulResponses! - before.sender.successfulResponses!) *
          initial.reader.readBatchSize.applied! / 30

        expect(after.sender.timeoutsTotal).toBe(0)
        expect(after.sender.terminalFailedTransactionsTotal).toBe(0)
        if (workers < 32) {
          expect(deliveredTps).toBeLessThan(250_000)
          expect(after.senderChannel.depthBatches).toBeGreaterThanOrEqual(
            after.senderChannel.capacity.applied! - 1,
          )
          expect(after.senderChannel.blockedMs).toBeGreaterThan(before.senderChannel.blockedMs!)
          expect(after.reader.limitationReason).toBe('downstream-backpressure')
        } else {
          // A long window absorbs batch completion and retry timing jitter.
          expect(deliveredTps).toBeGreaterThanOrEqual(249_000)
          expect(deliveredTps).toBeLessThanOrEqual(251_000)
          expect(before.senderChannel.depthBatches).toBe(0)
          expect(after.senderChannel.depthBatches).toBe(0)
          expect(after.senderChannel.blockedMs).toBe(before.senderChannel.blockedMs)
          expect(after.reader.limitationReason).toBeNull()
        }
      }
    } finally {
      adapter.dispose()
    }
  })

  it('keeps draining in-flight workers in aggregate and HTTP telemetry', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'set-sender-workers', value: 2 })
    await adapter.dispatch({ type: 'set-throttler-installed', value: false })
    expect(adapter.getSnapshot().throttler.installed.applied).toBe(false)
    await adapter.dispatch({ type: 'set-read-batch-size', value: 1_000 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(50)
    await adapter.dispatch({ type: 'set-sender-workers', value: 1 })

    expect(adapter.getSnapshot().sender).toMatchObject({
      liveWorkers: 2,
      inFlightWorkers: 2,
      drainingWorkers: 1,
      drainingInFlightWorkers: 1,
      inFlightRequests: 2,
    })
    expect(adapter.getSnapshot().http.inFlightRequests).toBe(2)
    const requestedTps = adapter.getSnapshot().throttler.requestedTps.applied
    await adapter.dispatch({ type: 'pause' })
    await adapter.dispatch({ type: 'reset' })
    expect(adapter.getSnapshot().throttler).toMatchObject({
      installed: { applied: false }, requestedTps: { applied: requestedTps },
    })
    await adapter.dispatch({ type: 'set-throttler-installed', value: true })
    expect(adapter.getSnapshot().throttler).toMatchObject({
      installed: { applied: true }, requestedTps: { applied: requestedTps },
    })
    adapter.dispose()
  })
})
