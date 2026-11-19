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
      liveWorkers: 3,
      idleWorkers: 3,
      inFlightWorkers: 0,
      backoffWorkers: 0,
      drainingWorkers: 0,
    })
    expect(Object.keys(snapshot.sender)).toEqual(
      expect.arrayContaining([
        'workers',
        'liveWorkers',
        'idleWorkers',
        'inFlightWorkers',
        'backoffWorkers',
        'drainingWorkers',
      ]),
    )
    adapter.dispose()
  })

  it('starts immediately and ticks once per interval across subscriptions', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()
    expect(adapter.getSnapshot()).toMatchObject({ revision: 0, runState: 'running' })

    const listener = vi.fn()
    adapter.subscribe(listener)()
    const unsubscribe = adapter.subscribe(listener)
    expect(listener).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(1)

    await vi.advanceTimersByTimeAsync(200)
    const snapshot = adapter.getSnapshot()
    expect(snapshot).toMatchObject({ revision: 2, elapsedMs: 200 })
    expect(snapshot.totalTransactions).toBeGreaterThan(0)
    expect(snapshot.senderChannel.outputTransactionsPerSecond).toBeGreaterThan(0)
    expect(listener).toHaveBeenCalledTimes(4)

    unsubscribe()
    await vi.advanceTimersByTimeAsync(100)
    expect(listener).toHaveBeenCalledTimes(4)

    adapter.dispose()
    adapter.dispose()
    const disposedSnapshot = adapter.getSnapshot()
    const afterDispose = vi.fn()
    adapter.subscribe(afterDispose)()
    await vi.advanceTimersByTimeAsync(300)
    expect(adapter.getSnapshot()).toBe(disposedSnapshot)
    expect(afterDispose).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    await expect(adapter.dispatch({ type: 'set-sender-workers', value: 4 })).resolves.toMatchObject(
      { accepted: false, error: { code: 'disposed' } },
    )
  })

  it('applies Reader, Sender, and TPS changes while running', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    await vi.advanceTimersByTimeAsync(200)
    const initial = adapter.getSnapshot()
    await expect(adapter.dispatch({ type: 'set-reader-workers', value: 7 })).resolves.toMatchObject(
      { accepted: true },
    )
    await expect(adapter.dispatch({ type: 'set-sender-workers', value: 4 })).resolves.toMatchObject(
      { accepted: true },
    )
    await expect(
      adapter.dispatch({ type: 'set-requested-tps', value: 75_000 }),
    ).resolves.toMatchObject({ accepted: true })

    const updated = adapter.getSnapshot()
    expect(updated).toMatchObject({
      runState: 'running',
      elapsedMs: initial.elapsedMs,
      reader: { workers: { applied: 7 }, liveWorkers: 7 },
      sender: { workers: { applied: 4 }, liveWorkers: 4 },
      throttler: { requestedTps: { applied: 75_000 } },
    })
    await vi.advanceTimersByTimeAsync(200)
    expect(adapter.getSnapshot().totalTransactions).toBeGreaterThan(initial.totalTransactions)
    adapter.dispose()
  })

  it('needs a near-maximum Sender pool to sustain maximum TPS and drain demo queues', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    try {
      const initial = adapter.getSnapshot()
      expect(initial.reader.workers.applied).toBe(5)
      expect(initial.reader.readBatchSize).toBe(1_000)
      expect(initial.sender.workers.applied).toBe(3)
      expect(initial.throttler.requestedTps.max).toBe(250_000)
      expect(initial.sender.workers.max).toBe(32)
      await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })

      for (const workers of [3, 24, 32]) {
        await adapter.dispatch({ type: 'set-sender-workers', value: workers })
        await vi.advanceTimersByTimeAsync(10_000)
        const before = adapter.getSnapshot()
        let sawBlockedSender = false
        for (let interval = 0; interval < 300; interval += 1) {
          await vi.advanceTimersByTimeAsync(100)
          sawBlockedSender ||= adapter.getSnapshot().senderChannel.blockedSenders > 0
        }
        const after = adapter.getSnapshot()
        const forwardedTps = (after.totalTransactions - before.totalTransactions) / 30
        if (workers < 32) {
          expect(forwardedTps).toBeLessThan(250_000)
          expect(after.senderChannel.depthBatches).toBeGreaterThanOrEqual(
            after.senderChannel.capacity! - 1,
          )
          expect(sawBlockedSender).toBe(true)
        } else {
          // A long window absorbs batch completion and retry timing jitter.
          expect(forwardedTps).toBeGreaterThanOrEqual(249_000)
          expect(forwardedTps).toBeLessThanOrEqual(251_000)
          expect(before.senderChannel.depthBatches).toBe(0)
          expect(after.senderChannel.depthBatches).toBe(0)
          expect(sawBlockedSender).toBe(false)
        }
      }
    } finally {
      adapter.dispose()
    }
  })

  it('keeps draining in-flight workers in the aggregate', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'set-sender-workers', value: 2 })
    await vi.advanceTimersByTimeAsync(50)
    await adapter.dispatch({ type: 'set-sender-workers', value: 1 })

    expect(adapter.getSnapshot().sender).toMatchObject({
      liveWorkers: 2,
      inFlightWorkers: 2,
      drainingWorkers: 1,
      drainingInFlightWorkers: 1,
    })
    adapter.dispose()
  })
})
