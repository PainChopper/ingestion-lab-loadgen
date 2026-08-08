import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type {
  LoadgenCommand,
  LoadgenSnapshot,
  NumericControlSnapshot,
} from '../model/loadgen'
import { SimulationAdapter } from './SimulationAdapter'

describe('SimulationAdapter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-06T09:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('emits the current immutable snapshot and unsubscribes cleanly', async () => {
    const adapter = new SimulationAdapter()
    const snapshots: LoadgenSnapshot[] = []
    const unsubscribe = adapter.subscribe((snapshot) => snapshots.push(snapshot))
    const initial = adapter.getSnapshot()

    expect(snapshots).toEqual([initial])
    expect(Object.isFrozen(initial)).toBe(true)
    expect(Object.isFrozen(initial.reader)).toBe(true)
    expect(Object.isFrozen(initial.reader.workers)).toBe(true)
    expect(Object.isFrozen(initial.queue2.capacity)).toBe(true)
    expect(Object.isFrozen(initial.target.errorRatePercent)).toBe(true)

    unsubscribe()
    await adapter.dispatch({ type: 'run' })
    expect(snapshots).toHaveLength(1)
    adapter.dispose()
  })

  it('applies every configuration command with snapshot clamp and step rules', async () => {
    const adapter = new SimulationAdapter()
    const cases: ReadonlyArray<{
      command: LoadgenCommand
      select: (snapshot: LoadgenSnapshot) => NumericControlSnapshot
      expected: number
    }> = [
      {
        command: { type: 'set-worker-count', actor: 'reader', value: -4 },
        select: (snapshot) => snapshot.reader.workers,
        expected: 1,
      },
      {
        command: { type: 'set-worker-count', actor: 'sender', value: 9 },
        select: (snapshot) => snapshot.sender.workers,
        expected: 7,
      },
      {
        command: { type: 'set-queue-capacity', queue: 'reader-to-throttler', value: 7.6 },
        select: (snapshot) => snapshot.queue1.capacity,
        expected: 8,
      },
      {
        command: { type: 'set-queue-capacity', queue: 'throttler-to-sender', value: 127 },
        select: (snapshot) => snapshot.queue2.capacity,
        expected: 130,
      },
      {
        command: { type: 'set-requested-tps', value: 122_600 },
        select: (snapshot) => snapshot.throttler.requestedTps,
        expected: 125_000,
      },
      {
        command: { type: 'set-read-batch-size', value: 12_500 },
        select: (snapshot) => snapshot.reader.readBatchSize,
        expected: 13_000,
      },
      {
        command: { type: 'set-http-batch-size', value: 1_050 },
        select: (snapshot) => snapshot.sender.httpBatchSize,
        expected: 1_100,
      },
      {
        command: { type: 'set-http-timeout', valueMs: 504 },
        select: (snapshot) => snapshot.sender.timeoutMs,
        expected: 500,
      },
      {
        command: { type: 'set-target-delay', valueMs: 44 },
        select: (snapshot) => snapshot.target.artificialDelayMs,
        expected: 40,
      },
      {
        command: { type: 'set-target-error-rate', valuePercent: 2.6 },
        select: (snapshot) => snapshot.target.errorRatePercent,
        expected: 3,
      },
    ]

    for (const testCase of cases) {
      const receipt = await adapter.dispatch(testCase.command)
      expect(receipt.accepted).toBe(true)
      expect(receipt.applyMode).toBe('immediate')
      expect(testCase.select(adapter.getSnapshot()).applied).toBe(
        testCase.expected,
      )
    }
    adapter.dispose()
  })

  it('rejects non-finite values for every configuration command', async () => {
    const adapter = new SimulationAdapter()
    const commands: LoadgenCommand[] = [
      { type: 'set-worker-count', actor: 'reader', value: Number.NaN },
      { type: 'set-queue-capacity', queue: 'reader-to-throttler', value: Infinity },
      { type: 'set-requested-tps', value: Number.NaN },
      { type: 'set-read-batch-size', value: Infinity },
      { type: 'set-http-batch-size', value: Number.NaN },
      { type: 'set-http-timeout', valueMs: Infinity },
      { type: 'set-target-delay', valueMs: Number.NaN },
      { type: 'set-target-error-rate', valuePercent: Infinity },
    ]
    const initial = adapter.getSnapshot()

    for (const command of commands) {
      const receipt = await adapter.dispatch(command)
      expect(receipt).toMatchObject({
        accepted: false,
        applyMode: 'unavailable',
        appliedAtMs: null,
        snapshotRevision: 0,
        error: { code: 'invalid-command', retryable: false },
      })
      expect(Object.isFrozen(receipt)).toBe(true)
      expect(Object.isFrozen(receipt.error)).toBe(true)
    }
    expect(adapter.getSnapshot()).toBe(initial)
    adapter.dispose()
  })

  it('publishes revisions only for changes and returns immutable receipts', async () => {
    const adapter = new SimulationAdapter()
    const initial = adapter.getSnapshot()

    const unchanged = await adapter.dispatch({
      type: 'set-requested-tps',
      value: 120_000,
    })
    expect(adapter.getSnapshot()).toBe(initial)
    expect(unchanged.snapshotRevision).toBe(0)

    const changed = await adapter.dispatch({
      type: 'set-requested-tps',
      value: 40_000,
    })
    expect(adapter.getSnapshot().revision).toBe(1)
    expect(changed).toMatchObject({
      commandId: 'simulation-2',
      commandType: 'set-requested-tps',
      accepted: true,
      applyMode: 'immediate',
      appliedAtMs: Date.now(),
      snapshotRevision: 1,
      error: null,
    })
    expect(Object.isFrozen(changed)).toBe(true)
    adapter.dispose()
  })

  it('runs, pauses, resumes, and resets counters while preserving configuration', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-read-batch-size', value: 30_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 80 })

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)
    expect(adapter.getSnapshot()).toMatchObject({
      runState: 'running',
      elapsedMs: 500,
      totalTransactions: 60_000,
    })

    await adapter.dispatch({ type: 'pause' })
    const paused = adapter.getSnapshot()
    await vi.advanceTimersByTimeAsync(500)
    expect(adapter.getSnapshot()).toBe(paused)
    expect(paused.queue1.throughputTps).toBe(0)
    expect(paused.queue1.flowState).toBe('stopped')

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().elapsedMs).toBe(600)

    await adapter.dispatch({ type: 'reset' })
    expect(adapter.getSnapshot()).toMatchObject({
      runState: 'idle',
      elapsedMs: 0,
      totalTransactions: 0,
      reader: { readBatchSize: { applied: 30_000 } },
      target: { artificialDelayMs: { applied: 80 } },
      sender: {
        successfulResponses: 0,
        failedResponses: 0,
        retries: 0,
      },
    })
    adapter.dispose()
  })

  it('integrates state across requested TPS changes without losing fixed steps', async () => {
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(250)
    const beforeChange = adapter.getSnapshot()
    await adapter.dispatch({ type: 'set-requested-tps', value: 40_000 })
    await vi.advanceTimersByTimeAsync(250)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.elapsedMs).toBe(500)
    expect(snapshot.queue1.enqueuedBatchesTotal).toBeGreaterThanOrEqual(
      beforeChange.queue1.enqueuedBatchesTotal,
    )
    expect(snapshot.queue1.dequeuedBatchesTotal).toBeGreaterThanOrEqual(
      beforeChange.queue1.dequeuedBatchesTotal,
    )
    expect(snapshot.totalTransactions).toBe(
      snapshot.queue1.dequeuedTransactionsTotal,
    )
    adapter.dispose()
  })

  it('does not lose elapsed time when run is dispatched repeatedly', async () => {
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(50)
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(50)

    expect(adapter.getSnapshot().elapsedMs).toBe(100)
    expect(adapter.getSnapshot().totalTransactions).toBe(0)
    expect(adapter.getSnapshot().queue1.depthBatches).toBe(0)
    adapter.dispose()
  })

  it('models active rendezvous flow at zero capacity without queue depth', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 0,
    })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 0,
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.depthBatches).toBe(0)
    expect(snapshot.queue2.depthBatches).toBe(0)
    expect(snapshot.queue1.handoffBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue2.handoffBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue1.inputTransactionsPerSecond).toBeGreaterThan(0)
    expect(snapshot.queue1.outputTransactionsPerSecond).toBeGreaterThan(0)
    expect(snapshot.queue2.outputTransactionsPerSecond).toBeGreaterThan(0)
    expect(snapshot.queue1.flowState).not.toBe('stopped')
    adapter.dispose()
  })

  it('reports a newly full queue as near-limit while no sender is blocked', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 1 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 1,
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.depthBatches).toBe(1)
    expect(snapshot.queue1.blockedSenders).toBe(0)
    expect(snapshot.queue1.oldestBlockedSenderMs).toBe(0)
    expect(snapshot.queue1.flowState).toBe('near-limit')
    adapter.dispose()
  })

  it('keeps reader worker capacity independent of read batch size', async () => {
    const fourWorkers = new SimulationAdapter()
    await fourWorkers.dispatch({ type: 'set-read-batch-size', value: 50_000 })
    await fourWorkers.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await fourWorkers.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 12,
    })
    await fourWorkers.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(fourWorkers.getSnapshot().reader.readTps).toBe(200_000)
    fourWorkers.dispose()

    const sevenWorkers = new SimulationAdapter()
    await sevenWorkers.dispatch({ type: 'set-worker-count', actor: 'reader', value: 7 })
    await sevenWorkers.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await sevenWorkers.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 12,
    })
    await sevenWorkers.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(sevenWorkers.getSnapshot().reader.readTps).toBe(350_000)
    sevenWorkers.dispose()
  })

  it('drains a full reader queue after reducing workers from four to one', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)

    const saturated = adapter.getSnapshot()
    expect(saturated.reader.workers.applied).toBe(4)
    expect(saturated.throttler.requestedTps.applied).toBe(120_000)
    expect(saturated.queue1.capacity.applied).toBe(4)
    expect(saturated.queue1.depthBatches).toBe(4)
    expect(saturated.queue1.blockedSenders).toBe(1)
    expect(saturated.queue1.flowState).toBe('backpressure')

    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 1 })
    await vi.advanceTimersByTimeAsync(2_000)

    const recovered = adapter.getSnapshot()
    expect(recovered.reader.readTps).toBe(50_000)
    expect(recovered.queue1.depthBatches).not.toBeNull()
    expect(saturated.queue1.depthBatches).not.toBeNull()
    expect(recovered.queue1.depthBatches!).toBeLessThan(
      saturated.queue1.depthBatches!,
    )
    expect(recovered.queue1.blockedSenders).toBe(0)
    expect(recovered.queue1.flowState).not.toBe('backpressure')
    adapter.dispose()
  })

  it('does not systematically admit above the requested transaction rate', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 12,
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)
    const before = adapter.getSnapshot()

    await vi.advanceTimersByTimeAsync(4_000)
    const after = adapter.getSnapshot()
    const averageAdmittedTps =
      (after.totalTransactions - before.totalTransactions) /
      ((after.elapsedMs - before.elapsedMs) / 1_000)

    expect(averageAdmittedTps).toBeLessThanOrEqual(120_000)
    expect(averageAdmittedTps).toBeGreaterThanOrEqual(115_000)
    adapter.dispose()
  })

  it('enters backpressure only after a sustained blocked send', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 1 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 1_000 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 1,
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_300)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.flowState).toBe('backpressure')
    expect(snapshot.queue1.blockedSenders).toBe(1)
    expect(snapshot.queue1.oldestBlockedSenderMs).toBeGreaterThanOrEqual(300)
    expect(snapshot.queue1.blockedMs).toBeGreaterThanOrEqual(300)

    await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().queue1.blockedSenders).toBe(0)
    expect(adapter.getSnapshot().queue1.flowState).toBe('backpressure')
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().queue1.flowState).not.toBe('backpressure')
    adapter.dispose()
  })

  it('does not create depth when worker count changes from one to seven', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 1 })
    const before = adapter.getSnapshot()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 7 })
    const after = adapter.getSnapshot()

    expect(before.queue1.depthBatches).toBe(0)
    expect(after.queue1.depthBatches).toBe(0)
    expect(after.queue1.enqueuedBatchesTotal).toBe(0)
    expect(after.reader.workers.applied).toBe(7)
    adapter.dispose()
  })

  it('does not create batches when capacity changes from zero to four', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 0,
    })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 4,
    })

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.capacity.applied).toBe(4)
    expect(snapshot.queue1.depthBatches).toBe(0)
    expect(snapshot.queue1.enqueuedBatchesTotal).toBe(0)
    expect(snapshot.queue1.queuedTransactions).toBe(0)
    adapter.dispose()
  })

  it('keeps live depth while a lower capacity is pending', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 1 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 1_000 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    const before = adapter.getSnapshot()

    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 0,
    })
    const after = adapter.getSnapshot()

    expect(before.queue1.depthBatches).toBeGreaterThan(0)
    expect(after.queue1.depthBatches).toBe(before.queue1.depthBatches)
    expect(after.queue1.capacity.applied).toBe(4)
    expect(after.queue1.capacity.preview).toBe(0)
    expect(after.queue1.capacity.pending).toBe(0)
    expect(after.queue1.enqueuedBatchesTotal).toBe(
      before.queue1.enqueuedBatchesTotal,
    )
    adapter.dispose()
  })

  it('tracks independent read and HTTP batch rates', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-read-batch-size', value: 25_000 })
    await adapter.dispatch({ type: 'set-http-batch-size', value: 1_000 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.dequeuedBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue2.enqueuedBatchesTotal).toBeGreaterThan(
      snapshot.queue1.dequeuedBatchesTotal,
    )
    expect(snapshot.queue2.inputBatchesPerSecond).toBeGreaterThan(
      snapshot.queue1.outputBatchesPerSecond,
    )
    expect(snapshot.queue1.dequeuedTransactionsTotal).toBeGreaterThanOrEqual(
      snapshot.queue2.enqueuedTransactionsTotal,
    )
    adapter.dispose()
  })

  it('keeps HTTP lifecycle counters internally consistent', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 50 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.http.requestsStartedTotal).toBeGreaterThan(0)
    expect(snapshot.http.requestsStartedTotal).toBe(
      snapshot.http.requestsCompletedTotal +
        (snapshot.http.inFlightRequests ?? 0),
    )
    expect(snapshot.http.requestsCompletedTotal).toBe(
      snapshot.http.requestsSucceededTotal + snapshot.http.requestsFailedTotal,
    )
    expect(snapshot.http.requestsSucceededTotal).toBeGreaterThan(0)
    expect(snapshot.http.requestsFailedTotal).toBeGreaterThan(0)
    adapter.dispose()
  })

  it('counts timeouts as failed HTTP requests without inventing 503 responses', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({ type: 'set-http-timeout', valueMs: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.http.statusCode).toBe(504)
    expect(snapshot.http.requestsFailedTotal).toBeGreaterThan(0)
    expect(snapshot.target.http503Responses).toBe(0)
    adapter.dispose()
  })

  it('preserves state across pause and reproduces the run after reset', async () => {
    const baseline = new SimulationAdapter()
    await baseline.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    const expected = baseline.getSnapshot()
    baseline.dispose()

    vi.setSystemTime(new Date('2026-08-06T09:00:00Z'))
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)
    await adapter.dispatch({ type: 'pause' })
    const paused = adapter.getSnapshot()
    await vi.advanceTimersByTimeAsync(500)
    expect(adapter.getSnapshot()).toBe(paused)

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)
    const resumed = adapter.getSnapshot()
    expect(resumed.elapsedMs).toBe(expected.elapsedMs)
    expect(resumed.queue1.enqueuedBatchesTotal).toBe(
      expected.queue1.enqueuedBatchesTotal,
    )
    expect(resumed.queue1.dequeuedBatchesTotal).toBe(
      expected.queue1.dequeuedBatchesTotal,
    )
    expect(resumed.queue2.enqueuedBatchesTotal).toBe(
      expected.queue2.enqueuedBatchesTotal,
    )
    expect(resumed.http.requestsStartedTotal).toBe(
      expected.http.requestsStartedTotal,
    )

    await adapter.dispatch({ type: 'reset' })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    const replay = adapter.getSnapshot()
    expect(replay.queue1.enqueuedBatchesTotal).toBe(
      expected.queue1.enqueuedBatchesTotal,
    )
    expect(replay.queue2.enqueuedBatchesTotal).toBe(
      expected.queue2.enqueuedBatchesTotal,
    )
    expect(replay.http.requestsStartedTotal).toBe(
      expected.http.requestsStartedTotal,
    )
    adapter.dispose()
  })

  it('stops timers and rejects commands after dispose', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(200)
    const snapshot = adapter.getSnapshot()

    adapter.dispose()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(500)
    expect(adapter.getSnapshot()).toBe(snapshot)

    const receipt = await adapter.dispatch({ type: 'pause' })
    expect(receipt.accepted).toBe(false)
    expect(receipt.error?.code).toBe('disposed')
    expect(receipt.snapshotRevision).toBe(snapshot.revision)
  })
})
