import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSimulationWireSnapshot, SimulationAdapter } from './SimulationAdapter'
import { FixedStepSimulation } from '../model/simulation'
import { wireSnapshotSchema } from '../model/wireSnapshot'

describe('SimulationAdapter', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('builds a full HTTP wire snapshot from simulation state and retains immutable config', () => {
    const simulation = new FixedStepSimulation(
      {
        readerWorkers: 1,
        senderWorkers: 1,
        requestedTps: 50_000,
        readBatchSize: 1_000,
        httpTimeoutMs: 500,
        targetDelayMs: 100,
        targetErrorRatePercent: 0,
      },
      2,
      3,
    )
    const first = createSimulationWireSnapshot(simulation)
    expect(wireSnapshotSchema.parse(first)).toEqual(first)
    expect(first).toMatchObject({
      capturedAtMs: 0,
      run: { state: 'running', elapsedMs: 0, totalTransactions: 0 },
      reader: {
        workers: 1,
        rowsRead: 0,
        sourceError: null,
        workerStates: [{ activity: 'idle', draining: false }],
      },
      throttler: { installed: true, requestedTps: 50_000 },
      sender: { workers: 1, workerStates: [{ activity: 'idle', draining: false }] },
      config: {
        readerWorkersMax: 7,
        senderWorkersMax: 32,
        requestedTpsMax: 250_000,
        readBatchSize: 1_000,
        readerChannelCapacity: 2,
        senderChannelCapacity: 3,
        sourcePath: '../data/MBD-mini/trx/fold=*/*.parquet',
      },
    })
    simulation.updateConfig({
      readerWorkers: 3,
      senderWorkers: 2,
      requestedTps: 0,
      throttlerInstalled: false,
    })
    for (let step = 0; step < 50; step += 1) simulation.advanceStep()
    const wire = createSimulationWireSnapshot(simulation)
    expect(wireSnapshotSchema.parse(wire)).toEqual(wire)
    expect(wire.config).toEqual(first.config)
    expect(wire.reader.workers).toBe(3)
    expect(wire.reader.workerStates).toHaveLength(3)
    expect(wire.sender.workers).toBe(2)
    expect(wire.sender.workerStates).toHaveLength(2)
    expect(wire.throttler).toEqual({ installed: false, requestedTps: 0 })
    expect(wire.run.totalTransactions).toBeGreaterThan(0)
    expect(wire.senderChannel.inputTransactionsTotal).toBeGreaterThan(0)
    expect(wire.readerChannel.blockedMs).toBeGreaterThan(0)
    expect(wire.capturedAtMs).toBe(wire.run.elapsedMs)
  })

  it('accepts exact integer TPS and rejects values outside the integer range', async () => {
    const adapter = new SimulationAdapter()
    for (const value of [0, 120_001, 250_000]) {
      expect((await adapter.dispatch({ type: 'set-requested-tps', value })).accepted).toBe(true)
      expect(adapter.getSnapshot().throttler.requestedTps.applied).toBe(value)
    }
    for (const value of [-1, 250_001, 1.5, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      expect((await adapter.dispatch({ type: 'set-requested-tps', value })).accepted).toBe(false)
      expect(adapter.getSnapshot().throttler.requestedTps.applied).toBe(250_000)
    }
    adapter.dispose()
  })

  it('publishes an immutable Sender worker list snapshot with retained controls', () => {
    const adapter = new SimulationAdapter()
    const snapshot = adapter.getSnapshot()

    expect(Object.isFrozen(snapshot)).toBe(true)
    expect(Object.isFrozen(snapshot.sender)).toBe(true)
    expect(snapshot.sender.workers.applied).not.toBeNull()
    expect(snapshot.sender.workerStates).toEqual([
      { activity: 'idle', draining: false },
      { activity: 'idle', draining: false },
      { activity: 'idle', draining: false },
    ])
    expect(Object.keys(snapshot.sender)).toEqual(['id', 'workers', 'workerStates'])
    for (const pool of [snapshot.reader, snapshot.sender]) {
      expect(Object.isFrozen(pool.workerStates)).toBe(true)
      expect(pool.workerStates.every(Object.isFrozen)).toBe(true)
    }
    adapter.dispose()
  })

  it('starts immediately and ticks once per interval across subscriptions', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()
    expect(adapter.getSnapshot()).toMatchObject({ revision: 0, runState: 'running' })
    expect(adapter.getSnapshot().reader.readTps).toBeNull()
    expect(adapter.getSnapshot().throttler.admittedTps).toBeNull()

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
    expect(snapshot.throttler.admittedTps).toBe(snapshot.senderChannel.inputTransactionsPerSecond)
    expect(adapter.getSnapshot()).toBe(snapshot)
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
    const initialWorkerStates = JSON.stringify([
      initial.reader.workerStates,
      initial.sender.workerStates,
    ])
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
      reader: { workers: { applied: 7 } },
      sender: { workers: { applied: 4 } },
      throttler: { requestedTps: { applied: 75_000 } },
    })
    expect(updated.reader.workerStates).toHaveLength(7)
    expect(updated.sender.workerStates).toHaveLength(4)
    await vi.advanceTimersByTimeAsync(200)
    expect(adapter.getSnapshot().totalTransactions).toBeGreaterThan(initial.totalTransactions)
    expect(JSON.stringify([initial.reader.workerStates, initial.sender.workerStates])).toBe(
      initialWorkerStates,
    )
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

  it('keeps draining in-flight workers in the worker list', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'set-sender-workers', value: 2 })
    await vi.advanceTimersByTimeAsync(50)
    await adapter.dispatch({ type: 'set-sender-workers', value: 1 })

    expect(adapter.getSnapshot().sender.workers.applied).toBe(1)
    expect(adapter.getSnapshot().sender.workerStates).toEqual(
      expect.arrayContaining([
        { activity: 'inFlight', draining: false },
        { activity: 'inFlight', draining: true },
      ]),
    )
    expect(adapter.getSnapshot().sender.workerStates).toHaveLength(2)
    adapter.dispose()
  })
})
