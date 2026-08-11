import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type {
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  SenderWorkerSlotSnapshot,
  SenderWorkerStateCounts,
} from '../model/loadgen'
import { QueueFlowStateDeriver } from '../model/queueFlowState'
import {
  deterministicRetryDelayMs,
  FixedStepSimulation,
  type SimulationAttemptContext,
  type SimulationAttemptOutcome,
  type SimulationConfig,
} from '../model/simulation'
import { SimulationAdapter } from './SimulationAdapter'

const DIRECT_CONFIG: SimulationConfig = {
  readerWorkers: 1,
  senderWorkers: 1,
  requestedTps: 50_000,
  throttlerInstallationMode: 'installed',
  readBatchSize: 1_000,
  httpBatchSize: 1_000,
  httpTimeoutMs: 500,
  targetDelayMs: 5,
  targetErrorRatePercent: 0,
}

function advanceUntil(
  simulation: FixedStepSimulation,
  predicate: () => boolean,
  maxSteps = 10_000,
): void {
  for (let step = 0; step < maxSteps && !predicate(); step += 1) {
    simulation.advanceStep()
  }
  expect(predicate()).toBe(true)
}

function workerSlotCounts(
  slots: readonly SenderWorkerSlotSnapshot[],
): SenderWorkerStateCounts {
  return {
    idle: slots.filter(({ state }) => state === 'idle').length,
    inFlight: slots.filter(({ state }) => state === 'in-flight').length,
    backoff: slots.filter(({ state }) => state === 'backoff').length,
  }
}

function expectSenderSlotConservation(sender: {
  readonly workerSlots: readonly SenderWorkerSlotSnapshot[] | null
  readonly workerStates: SenderWorkerStateCounts
}): void {
  const slots = sender.workerSlots ?? []
  expect(sender.workerSlots).not.toBeNull()
  expect(workerSlotCounts(slots)).toEqual(sender.workerStates)
  expect(slots.map(({ ordinal }) => ordinal))
    .toEqual(slots.map((_, ordinal) => ordinal))
  expect(new Set(slots.map(({ id }) => id)).size).toBe(slots.length)
}

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
    const snapshots: LoadgenTelemetrySnapshot[] = []
    const unsubscribe = adapter.subscribe((snapshot) => snapshots.push(snapshot))
    const initial = adapter.getSnapshot()

    expect(snapshots).toEqual([initial])
    expect(Object.isFrozen(initial)).toBe(true)
    expect(Object.isFrozen(initial.reader)).toBe(true)
    expect(Object.isFrozen(initial.reader.workers)).toBe(true)
    expect(Object.isFrozen(initial.queue2.capacity)).toBe(true)
    expect(Object.isFrozen(initial.target.errorRatePercent)).toBe(true)
    expect(Object.isFrozen(initial.sender.workerSlots)).toBe(true)
    expect(Object.isFrozen(initial.sender.workerSlots?.[0])).toBe(true)
    expectSenderSlotConservation(initial.sender)

    unsubscribe()
    await adapter.dispatch({ type: 'run' })
    expect(snapshots).toHaveLength(1)
    adapter.dispose()
  })

  it('applies every configuration command with snapshot clamp and step rules', async () => {
    const adapter = new SimulationAdapter()
    const cases: ReadonlyArray<{
      command: LoadgenCommand
      select: (snapshot: LoadgenTelemetrySnapshot) => NumericControlSnapshot
      expected: number
    }> = [
      {
        command: { type: 'set-worker-count', actor: 'reader', value: -4 },
        select: (snapshot) => snapshot.reader.workers,
        expected: 1,
      },
      {
        command: { type: 'set-worker-count', actor: 'reader', value: 99 },
        select: (snapshot) => snapshot.reader.workers,
        expected: 7,
      },
      {
        command: { type: 'set-worker-count', actor: 'sender', value: 99 },
        select: (snapshot) => snapshot.sender.workers,
        expected: 32,
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

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().elapsedMs).toBe(600)

    await adapter.dispatch({ type: 'pause' })
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

  it('keeps q1 queued behind a zero throttle while q2 and HTTP drain', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-http-timeout', valueMs: 5_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 400 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(3_000)

    const saturated = adapter.getSnapshot()
    expect(saturated.queue1.depthBatches).toBe(4)
    expect(saturated.queue2.depthBatches).toBeGreaterThan(0)

    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    const closed = adapter.getSnapshot()
    const q1DequeuedAtClose = closed.queue1.dequeuedBatchesTotal
    await vi.advanceTimersByTimeAsync(20_000)

    const drained = adapter.getSnapshot()
    expect(drained.throttler.admittedTps).toBe(0)
    expect(drained.queue1.depthBatches).toBe(4)
    expect(drained.queue1.dequeuedBatchesTotal).toBe(q1DequeuedAtClose)
    expect(drained.queue2.depthBatches).toBe(0)
    expect(drained.http.inFlightRequests).toBe(0)
    adapter.dispose()
  })

  it('maps configured reader capacity independently from actual throughput', async () => {
    const adapter = new SimulationAdapter()

    await adapter.dispatch({
      type: 'set-worker-count',
      actor: 'reader',
      value: 1,
    })
    expect(adapter.getSnapshot().reader).toMatchObject({
      readTps: 0,
      configuredCapacityTps: 50_000,
    })

    await adapter.dispatch({
      type: 'set-worker-count',
      actor: 'reader',
      value: 7,
    })
    expect(adapter.getSnapshot().reader).toMatchObject({
      readTps: 0,
      configuredCapacityTps: 350_000,
    })
    adapter.dispose()
  })

  it('removes the installed throttle only after applied bypass', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 5_000 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 12,
    })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const installed = adapter.getSnapshot()
    expect(installed.throttler.installationMode.applied).toBe('installed')
    expect(installed.queue1.depthBatches).toBe(12)
    expect(installed.queue1.dequeuedBatchesTotal).toBe(0)

    await adapter.dispatch({
      type: 'set-throttler-installation-mode',
      value: 'bypass',
    })
    const applied = adapter.getSnapshot()
    expect(applied.throttler.requestedTps.applied).toBe(0)
    expect(applied.throttler.installationMode).toMatchObject({
      applied: 'bypass',
      pending: null,
    })
    expect(applied.queue1.dequeuedBatchesTotal).toBe(0)

    await vi.advanceTimersByTimeAsync(500)
    const bypass = adapter.getSnapshot()
    expect(bypass.queue1.dequeuedBatchesTotal).toBeGreaterThan(0)
    expect(bypass.queue2.depthBatches).toBeGreaterThan(0)
    expect(bypass.http.inFlightRequests).toBe(1)
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 0 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await vi.advanceTimersByTimeAsync(1_000)
    expect(adapter.getSnapshot().queue2.depthBatches).toBe(0)
    await adapter.dispatch({ type: 'reset' })
    expect(adapter.getSnapshot().throttler).toMatchObject({
      requestedTps: { applied: 0 },
      installationMode: { applied: 'bypass', pending: null },
    })
    adapter.dispose()
  })

  it('reports saturated bypass reading as downstream limited', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({
      type: 'set-worker-count',
      actor: 'reader',
      value: 7,
    })
    await adapter.dispatch({
      type: 'set-throttler-installation-mode',
      value: 'bypass',
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(50_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.reader).toMatchObject({
      readTps: 50_000,
      configuredCapacityTps: 350_000,
      limitationReason: 'downstream-backpressure',
    })
    expect(snapshot.reader.readTps).toBeLessThan(
      snapshot.reader.configuredCapacityTps ?? 0,
    )
    expect(snapshot.queue1.depthBatches).toBe(snapshot.queue1.capacity.applied)
    expect(snapshot.queue2.depthBatches).toBe(snapshot.queue2.capacity.applied)
    expect(snapshot.queue1.blockedSenders).toBe(1)
    expect(snapshot.queue2.blockedSenders).toBe(1)
    adapter.dispose()
  })

  it('keeps retry ownership and bounded queue pressure under bypass', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 7 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({
      type: 'set-throttler-installation-mode',
      value: 'bypass',
    })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.sender.workerStates.idle).toBe(0)
    expect(snapshot.sender.retryAttemptsStartedTotal).toBeGreaterThan(0)
    expect(snapshot.queue2.depthBatches).toBe(snapshot.queue2.capacity.applied)
    expect(snapshot.queue1.depthBatches).toBe(snapshot.queue1.capacity.applied)
    expect(snapshot.reader.limitationReason).toBe('downstream-backpressure')
    adapter.dispose()
  })

  it('reinserts at admission while admitted work continues downstream', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-requested-tps', value: 0 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 25_000 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 10,
    })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)
    await adapter.dispatch({
      type: 'set-throttler-installation-mode',
      value: 'bypass',
    })
    await vi.advanceTimersByTimeAsync(300)

    const beforeReinsert = adapter.getSnapshot()
    expect(beforeReinsert.queue2.depthBatches).toBe(10)
    await adapter.dispatch({
      type: 'set-throttler-installation-mode',
      value: 'installed',
    })
    const appliedReinsert = adapter.getSnapshot()
    const q1DequeuedAtReinsert = appliedReinsert.queue1.dequeuedBatchesTotal
    const q2EnqueuedAtReinsert = appliedReinsert.queue2.enqueuedBatchesTotal

    await adapter.dispatch({ type: 'set-target-delay', valueMs: 0 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await vi.advanceTimersByTimeAsync(500)
    const afterReinsert = adapter.getSnapshot()

    expect(afterReinsert.throttler.installationMode.applied).toBe('installed')
    expect(afterReinsert.queue1.dequeuedBatchesTotal).toBe(q1DequeuedAtReinsert)
    expect(afterReinsert.queue2.enqueuedBatchesTotal)
      .toBeGreaterThan(q2EnqueuedAtReinsert)
    expect(afterReinsert.queue2.depthBatches).toBe(0)
    adapter.dispose()
  })

  it('drains recovered q2 and releases backpressure when service exceeds input', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-http-timeout', valueMs: 5_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(3_000)
    const saturated = adapter.getSnapshot()
    expect(saturated.queue2.depthBatches).toBeGreaterThan(80)

    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({
      type: 'set-worker-count',
      actor: 'sender',
      value: 7,
    })
    await vi.advanceTimersByTimeAsync(4_000)
    const draining = adapter.getSnapshot()
    let minimumDepth = draining.queue2.depthBatches ?? Number.POSITIVE_INFINITY
    for (let index = 0; index < 60; index += 1) {
      await vi.advanceTimersByTimeAsync(100)
      minimumDepth = Math.min(
        minimumDepth,
        adapter.getSnapshot().queue2.depthBatches ?? Number.POSITIVE_INFINITY,
      )
    }

    const recovered = adapter.getSnapshot()
    expect(draining.queue2.depthBatches).toBeLessThan(
      saturated.queue2.depthBatches ?? 0,
    )
    expect(draining.queue2.depthBatches).toBeGreaterThan(0)
    expect(minimumDepth).toBe(0)
    expect(recovered.queue2.depthBatches).toBeLessThanOrEqual(25)
    expect(recovered.queue2.blockedSenders).toBe(0)
    adapter.dispose()
  })

  it('limits each sender worker to one in-flight HTTP request', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 7 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 7 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.http.inFlightRequests).toBeLessThanOrEqual(7)
    expect(snapshot.sender.inFlightRequests).toBeLessThanOrEqual(7)
    expect(snapshot.http.throughputTps).toBe(140_000)
    expect(snapshot.http.throughputTps).toBeLessThan(250_000)
    adapter.dispose()
  })

  it('keeps bottleneck throughput independent of q2 capacity', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await adapter.dispatch({ type: 'set-http-timeout', valueMs: 5_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 10,
    })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)
    const before = adapter.getSnapshot()

    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 160,
    })
    await vi.advanceTimersByTimeAsync(2_000)
    const after = adapter.getSnapshot()

    expect(before.queue2.depthBatches).toBe(10)
    expect(after.queue2.depthBatches).toBe(160)
    expect(after.http.throughputTps).toBe(before.http.throughputTps)
    expect(after.queue2.outputTransactionsPerSecond).toBe(
      before.queue2.outputTransactionsPerSecond,
    )
    adapter.dispose()
  })

  it('models active rendezvous flow at zero capacity without queue depth', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 5 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 7 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 0 })
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
    await vi.advanceTimersByTimeAsync(2_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.queue1.depthBatches).toBe(0)
    expect(snapshot.queue2.depthBatches).toBe(0)
    expect(snapshot.queue1.handoffBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue2.handoffBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue1.inputTransactionsPerSecond).toBe(250_000)
    expect(snapshot.queue1.outputTransactionsPerSecond).toBe(250_000)
    expect(snapshot.queue2.outputTransactionsPerSecond).toBe(250_000)
    adapter.dispose()
  })

  it('reports rendezvous backpressure under excess upstream load', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 7 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 5_000 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
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
    const derived = new QueueFlowStateDeriver().derive(snapshot, 0)
    expect(snapshot.sender.workers.applied).toBe(1)
    expect(snapshot.sender.inFlightRequests).toBe(1)
    expect(snapshot.queue1.depthBatches).toBe(0)
    expect(snapshot.queue2.depthBatches).toBe(0)
    expect(snapshot.queue1.blockedSenders).toBeGreaterThan(0)
    expect(snapshot.queue2.blockedSenders).toBeGreaterThan(0)
    expect(snapshot.queue1.handoffBatchesTotal).toBeGreaterThan(1)
    expect(snapshot.queue2.handoffBatchesTotal).toBeGreaterThan(1)
    expect(snapshot.queue1.oldestBlockedSenderMs).toBeLessThan(100)
    expect(snapshot.queue2.oldestBlockedSenderMs).toBeLessThan(100)
    expect(derived.queue2).toMatchObject({
      displayedPressure: 1,
      flowState: 'backpressure',
    })
    adapter.dispose()
  })

  it('does not systematically admit above the requested transaction rate', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 7 })
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
    expect(snapshot.http.requestsFailedTotal).toBe(
      (snapshot.sender.failedResponses ?? 0) +
        snapshot.http.requestsTimedOutTotal +
        snapshot.http.networkErrorsTotal,
    )
    expect(snapshot.http.requestsSucceededTotal).toBeGreaterThan(0)
    expect(snapshot.http.requestsFailedTotal).toBeGreaterThan(0)
    adapter.dispose()
  })

  it('maps actual rolling failed transaction throughput to the target', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 5 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 250_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 0 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 2 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.sender.attemptedTps).toBe(255_000)
    expect(snapshot.sender.retryAttemptedTps).toBe(5_000)
    expect(snapshot.target.acceptedTps).toBe(250_000)
    expect(snapshot.target.rejectedTps).toBe(5_000)
    adapter.dispose()
  })

  it('counts timeouts as failed HTTP requests without inventing 503 responses', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 2_000 })
    await adapter.dispatch({ type: 'set-http-timeout', valueMs: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(2_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.http.statusCode).toBeNull()
    expect(snapshot.http.lastOutcome).toBe('timeout')
    expect(snapshot.http.requestsFailedTotal).toBeGreaterThan(0)
    expect(snapshot.http.requestsTimedOutTotal).toBeGreaterThan(0)
    expect(snapshot.target.http503Responses).toBe(0)
    expect(snapshot.sender.ambiguousTimeoutTransactionsTotal).toBeGreaterThan(0)
    expect(snapshot.sender.duplicateRiskTransactionsTotal).toBeGreaterThan(0)
    expect(snapshot.sender.ambiguousTerminalTransactionsTotal).toBeGreaterThan(0)
    adapter.dispose()
  })

  it('replays deterministic telemetry after pause, resume, and reset', async () => {
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
    expect([
      resumed.elapsedMs,
      resumed.queue1.enqueuedBatchesTotal,
      resumed.queue1.dequeuedBatchesTotal,
      resumed.queue2.enqueuedBatchesTotal,
      resumed.http.requestsStartedTotal,
      resumed.sender.workerSlots,
    ]).toEqual([
      expected.elapsedMs,
      expected.queue1.enqueuedBatchesTotal,
      expected.queue1.dequeuedBatchesTotal,
      expected.queue2.enqueuedBatchesTotal,
      expected.http.requestsStartedTotal,
      expected.sender.workerSlots,
    ])

    await adapter.dispatch({ type: 'pause' })
    await adapter.dispatch({ type: 'reset' })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)
    const replay = adapter.getSnapshot()
    expect([
      replay.queue1.enqueuedBatchesTotal,
      replay.queue2.enqueuedBatchesTotal,
      replay.http.requestsStartedTotal,
      replay.sender.workerSlots,
    ]).toEqual([
      expected.queue1.enqueuedBatchesTotal,
      expected.queue2.enqueuedBatchesTotal,
      expected.http.requestsStartedTotal,
      expected.sender.workerSlots,
    ])
    adapter.dispose()
  })

  it('uses all exact deterministic jitter buckets without PRNG state', () => {
    expect([4, 0, 1, 2, 3].map((sequence) =>
      deterministicRetryDelayMs(sequence, 1),
    )).toEqual([200, 230, 250, 280, 300])
    expect([3, 4, 0, 1, 2].map((sequence) =>
      deterministicRetryDelayMs(sequence, 2),
    )).toEqual([400, 450, 500, 550, 600])
  })

  it('retains one batch identity and q2 dequeue across three attempts', () => {
    const attempts: SimulationAttemptContext[] = []
    const source = (
      context: SimulationAttemptContext,
    ): SimulationAttemptOutcome => {
      attempts.push(context)
      return {
        kind: 'http-response',
        statusCode: context.attempt === 3 ? 200 : 503,
        latencyMs: 10,
      }
    }
    const simulation = new FixedStepSimulation(DIRECT_CONFIG, 4, 10, source)

    advanceUntil(simulation, () => attempts.length === 3)
    const duringThirdAttempt = simulation.telemetry(true)

    expect(attempts.map(({ batch }) => [
      batch.sequence,
      batch.identity,
      batch.transactions,
    ])).toEqual([
      [0, 'http-batch-0', 1_000],
      [0, 'http-batch-0', 1_000],
      [0, 'http-batch-0', 1_000],
    ])
    expect(
      attempts[1].startedAtMs - attempts[0].startedAtMs - 10,
    ).toBe(230)
    expect(
      attempts[2].startedAtMs - attempts[1].startedAtMs - 10,
    ).toBe(500)
    expect(duringThirdAttempt.queue2.dequeuedBatchesTotal).toBe(1)
    expect(duringThirdAttempt.http.requestsStartedTotal).toBe(3)
    expect(duringThirdAttempt.sender.retryAttemptsStartedTotal).toBe(2)
    expect(duringThirdAttempt.sender.workerStates).toEqual({
      idle: 0,
      inFlight: 1,
      backoff: 0,
    })
    expect(duringThirdAttempt.sender.workerSlots).toEqual([{
      id: 'sender-worker-0',
      ordinal: 0,
      state: 'in-flight',
    }])

    simulation.advanceStep()
    const accepted = simulation.telemetry(true)
    expect(accepted.http.requestsSucceededTotal).toBe(1)
    expect(accepted.sender.terminalFailedBatchesTotal).toBe(0)
  })

  it('starts due retries before same-step FIFO work in worker order', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      readerWorkers: 7,
      senderWorkers: 2,
      requestedTps: 250_000,
      readBatchSize: 10_000,
    }, 4, 20, (context) => {
      attempts.push(context)
      return {
        kind: 'http-response',
        statusCode:
          context.batch.sequence === 0 && context.attempt === 1 ? 503 : 200,
        latencyMs: 10,
      }
    })

    advanceUntil(
      simulation,
      () => attempts.some(
        ({ batch, attempt }) => batch.sequence === 0 && attempt === 2,
      ),
    )
    const retry = attempts.find(
      ({ batch, attempt }) => batch.sequence === 0 && attempt === 2,
    )!
    const sameStepStarts = attempts.filter(
      ({ startedAtMs }) => startedAtMs === retry.startedAtMs,
    )

    expect(attempts.slice(0, 2).map(({ batch }) => batch.sequence))
      .toEqual([0, 1])
    expect(sameStepStarts[0]).toMatchObject({
      batch: { sequence: 0, identity: 'http-batch-0' },
      attempt: 2,
    })
    expect(sameStepStarts.slice(1).some(({ attempt }) => attempt === 1))
      .toBe(true)
  })

  it('projects different real slots in backoff with count conservation', () => {
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      readerWorkers: 7,
      senderWorkers: 2,
      requestedTps: 250_000,
      readBatchSize: 10_000,
    }, 4, 20, () => ({
      kind: 'http-response',
      statusCode: 503,
      latencyMs: 10,
    }))

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).sender.workerStates.backoff === 2,
    )
    const sender = simulation.telemetry(true).sender

    expectSenderSlotConservation(sender)
    expect(
      sender.workerSlots
        .filter(({ state }) => state === 'backoff')
        .map(({ id }) => id),
    ).toEqual(['sender-worker-0', 'sender-worker-1'])
  })

  it('keeps the same real slot identity through backoff and retry', () => {
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      senderWorkers: 2,
    }, 4, 10, (context) => ({
      kind: 'http-response',
      statusCode: context.batch.sequence === 0 && context.attempt === 1
        ? 503
        : 200,
      latencyMs: context.batch.sequence === 1 ? 1_000 : 10,
    }))

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).sender.workerStates.backoff === 1,
    )
    const backoffSlot = simulation.telemetry(true).sender.workerSlots.find(
      ({ state }) => state === 'backoff',
    )!

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).sender.retryAttemptsStartedTotal === 1,
    )
    const retrySlot = simulation.telemetry(true).sender.workerSlots.find(
      ({ id }) => id === backoffSlot.id,
    )!

    expect(retrySlot).toEqual({ ...backoffSlot, state: 'in-flight' })
  })

  it.each([0, 10])(
    'schedules available workers with deterministic round-robin fairness at q2 capacity %i',
    (queue2Capacity) => {
      const simulation = new FixedStepSimulation({
        ...DIRECT_CONFIG,
        senderWorkers: 3,
        requestedTps: 1_000,
      }, 4, queue2Capacity, () => ({
        kind: 'http-response',
        statusCode: 200,
        latencyMs: 10,
      }))
      const scheduledOrdinals: number[] = []

      for (let request = 1; request <= 6; request += 1) {
        advanceUntil(
          simulation,
          () => simulation.telemetry(true).http.requestsStartedTotal === request,
        )
        scheduledOrdinals.push(
          simulation.telemetry(true).sender.workerSlots.find(
            ({ state }) => state === 'in-flight',
          )!.ordinal,
        )
        advanceUntil(
          simulation,
          () => simulation.telemetry(true).http.requestsCompletedTotal === request,
        )
      }

      expect(scheduledOrdinals).toEqual([0, 1, 2, 0, 1, 2])
    },
  )

  it('allows documented cross-worker completion reorder after FIFO starts', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      readerWorkers: 7,
      senderWorkers: 2,
      requestedTps: 250_000,
      readBatchSize: 10_000,
    }, 4, 20, (context) => {
      attempts.push(context)
      return {
        kind: 'http-response',
        statusCode: 200,
        latencyMs: context.batch.sequence === 0 ? 30 : 10,
      }
    })

    advanceUntil(simulation, () => attempts.length >= 3)

    expect(attempts.slice(0, 2).map(({ batch }) => batch.sequence))
      .toEqual([0, 1])
    expect(attempts[2].batch.sequence).toBe(2)
    expect(attempts[2].startedAtMs).toBe(attempts[1].startedAtMs + 10)
    expect(attempts[2].startedAtMs).toBeLessThan(
      attempts[0].startedAtMs + 30,
    )
  })

  it('exhausts retryable failures at three attempts and stops on 400', () => {
    const retryableAttempts: SimulationAttemptContext[] = []
    const retryable = new FixedStepSimulation(
      DIRECT_CONFIG,
      4,
      10,
      (context) => {
        retryableAttempts.push(context)
        return { kind: 'http-response', statusCode: 503, latencyMs: 10 }
      },
    )
    advanceUntil(
      retryable,
      () => retryable.telemetry(true).sender.terminalFailedBatchesTotal === 1,
    )
    const exhausted = retryable.telemetry(true)
    expect(retryableAttempts.slice(0, 3).map(({ attempt }) => attempt))
      .toEqual([1, 2, 3])
    expect(exhausted.sender.retryAttemptsStartedTotal).toBe(2)
    expect(exhausted.sender.terminalFailedTransactionsTotal).toBe(1_000)

    const nonRetryableAttempts: SimulationAttemptContext[] = []
    const nonRetryable = new FixedStepSimulation(
      DIRECT_CONFIG,
      4,
      10,
      (context) => {
        nonRetryableAttempts.push(context)
        return { kind: 'http-response', statusCode: 400, latencyMs: 10 }
      },
    )
    advanceUntil(
      nonRetryable,
      () => nonRetryable.telemetry(true).sender.terminalFailedBatchesTotal === 1,
    )
    expect(nonRetryableAttempts).toHaveLength(1)
    expect(nonRetryable.telemetry(true).sender.retryAttemptsStartedTotal).toBe(0)
  })

  it('freezes an attempt outcome at start across target recovery', () => {
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      targetDelayMs: 40,
      targetErrorRatePercent: 100,
    }, 4, 10)

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.inFlightRequests === 1,
    )
    simulation.updateConfig({ targetErrorRatePercent: 0 })
    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.responsesRejectedTotal === 1,
    )
    const rejected = simulation.telemetry(true)
    expect(rejected.http.latestStatusCode).toBe(503)
    expect(rejected.sender.workerStates.backoff).toBe(1)

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.requestsSucceededTotal === 1,
    )
    const recovered = simulation.telemetry(true)
    expect(recovered.sender.retryAttemptsStartedTotal).toBe(1)
    expect(recovered.sender.terminalFailedBatchesTotal).toBe(0)
  })

  it('applies timeout updates only to attempts started after the command', () => {
    const simulation = new FixedStepSimulation({
      ...DIRECT_CONFIG,
      httpTimeoutMs: 50,
      targetDelayMs: 95,
    }, 4, 10)

    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.inFlightRequests === 1,
    )
    simulation.updateConfig({ httpTimeoutMs: 200 })
    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.requestsSucceededTotal === 1,
    )
    const recovered = simulation.telemetry(true)

    expect(recovered.http.requestsTimedOutTotal).toBe(1)
    expect(recovered.sender.retryAttemptsStartedTotal).toBe(1)
    expect(recovered.http.requestsSucceededTotal).toBe(1)
    expect(recovered.http.latestStatusCode).toBe(200)
  })

  it('retains formed batch sizes while later batches use new configuration', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(
      DIRECT_CONFIG,
      4,
      0,
      (context) => {
        attempts.push(context)
        return {
          kind: 'http-response',
          statusCode:
            context.batch.sequence === 0 && context.attempt === 1 ? 503 : 200,
          latencyMs: 10,
        }
      },
    )
    advanceUntil(
      simulation,
      () =>
        attempts.length === 1 &&
        simulation.telemetry(true).sender.workerStates.backoff === 1 &&
        simulation.telemetry(true).queue2.blockedSenders === 1,
    )
    simulation.updateConfig({ httpBatchSize: 500 })
    advanceUntil(
      simulation,
      () => attempts.some(({ batch }) => batch.sequence === 2),
    )

    expect(
      attempts.filter(({ batch }) => batch.sequence === 0)
        .map(({ batch }) => batch.transactions),
    ).toEqual([1_000, 1_000])
    expect(
      attempts.find(({ batch }) => batch.sequence === 1)?.batch.transactions,
    ).toBe(1_000)
    expect(
      attempts.find(({ batch }) => batch.sequence === 2)?.batch.transactions,
    ).toBe(500)
  })

  it('fills q2 and q1 under the owner 503 scenario then drains q2 on recovery', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 125_000 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 100,
    })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(4_000)

    const saturated = adapter.getSnapshot()
    expect(saturated.sender.workerStates.idle).toBe(0)
    expect(
      saturated.sender.workerStates.inFlight +
        saturated.sender.workerStates.backoff,
    ).toBe(32)
    expect(saturated.queue2.depthBatches).toBe(100)
    expect(saturated.queue1.depthBatches).toBe(saturated.queue1.capacity.applied)
    expect(saturated.reader.limitationReason).toBe('downstream-backpressure')
    expect(saturated.target.acceptedTps).toBe(0)
    expect(saturated.target.rejectedTps).toBeGreaterThan(0)
    expect(saturated.sender.retryAttemptsStartedTotal).toBeGreaterThan(0)
    expect(saturated.sender.terminalFailedBatchesTotal).toBeGreaterThan(0)
    expect(saturated.http.requestsStartedTotal).toBe(
      saturated.queue2.dequeuedBatchesTotal +
        saturated.sender.retryAttemptsStartedTotal,
    )
    expect(saturated.queue2.dequeuedBatchesTotal).toBe(
      saturated.http.requestsSucceededTotal +
        saturated.sender.terminalFailedBatchesTotal +
        saturated.sender.workerStates.inFlight +
        saturated.sender.workerStates.backoff,
    )

    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await vi.advanceTimersByTimeAsync(5_000)
    const recovered = adapter.getSnapshot()
    expect(recovered.target.acceptedTps).toBeGreaterThanOrEqual(125_000)
    expect(recovered.queue2.depthBatches).toBe(0)
    expect(recovered.queue2.blockedSenders).toBe(0)
    adapter.dispose()
  })

  it('drains q1 after recovery when source input remains below admission', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'reader', value: 2 })
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 125_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(4_000)

    const saturated = adapter.getSnapshot()
    expect(saturated.queue2.depthBatches).toBe(100)
    expect(saturated.queue1.depthBatches).toBe(saturated.queue1.capacity.applied)
    expect(saturated.reader.limitationReason).toBe('downstream-backpressure')

    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await vi.advanceTimersByTimeAsync(5_000)
    const recovered = adapter.getSnapshot()
    expect(recovered.queue2.depthBatches).toBe(0)
    expect(recovered.queue1.depthBatches).toBe(0)
    expect(recovered.reader.limitationReason).toBeNull()
    adapter.dispose()
  })

  it('keeps sender scale-down pending until owned retry lifecycles finish', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 125_000 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(500)

    const beforeScaleDown = adapter.getSnapshot()
    expectSenderSlotConservation(beforeScaleDown.sender)
    expect(
      beforeScaleDown.sender.workerStates.inFlight +
        beforeScaleDown.sender.workerStates.backoff,
    ).toBeGreaterThan(1)

    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    const pending = adapter.getSnapshot()
    expectSenderSlotConservation(pending.sender)
    expect(pending.sender.workers).toMatchObject({ applied: 32, pending: 1 })
    expect(
      pending.sender.workerStates.inFlight + pending.sender.workerStates.backoff,
    ).toBeGreaterThan(1)

    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await vi.advanceTimersByTimeAsync(2_000)
    const applied = adapter.getSnapshot()
    expectSenderSlotConservation(applied.sender)
    expect(applied.sender.workers).toMatchObject({ applied: 1, pending: null })
    expect(
      applied.sender.workerStates.idle +
        applied.sender.workerStates.inFlight +
        applied.sender.workerStates.backoff,
    ).toBe(1)
    expect(applied.sender.terminalFailedBatchesTotal).toBe(0)
    expect(applied.sender.workerSlots).toEqual([{
      id: 'sender-worker-0',
      ordinal: 0,
      state: expect.stringMatching(/^(idle|in-flight|backoff)$/),
    }])
    adapter.dispose()
  })

  it('does not promise q2 growth below the slowest 503 service boundary', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 32 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 5_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(20_000)

    const snapshot = adapter.getSnapshot()
    expect(snapshot.sender.terminalFailedBatchesTotal).toBeGreaterThan(0)
    expect(snapshot.queue2.depthBatches).toBe(0)
    expect(snapshot.queue2.blockedSenders).toBe(0)
    adapter.dispose()
  })

  it('keeps q2 saturated when recovered service remains below admission', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 125_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(4_000)
    expect(adapter.getSnapshot().queue2.depthBatches).toBe(100)

    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 0 })
    await vi.advanceTimersByTimeAsync(5_000)
    const recovered = adapter.getSnapshot()
    expect(recovered.target.acceptedTps).toBe(20_000)
    expect(recovered.queue2.depthBatches).toBe(100)
    expect(recovered.queue2.blockedSenders).toBe(1)
    adapter.dispose()
  })

  it('applies rendezvous pressure while the only worker owns retry work', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 125_000 })
    await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'throttler-to-sender',
      value: 0,
    })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 40 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(1_000)

    const snapshot = adapter.getSnapshot()
    const derived = new QueueFlowStateDeriver().derive(snapshot, 0)
    expect(snapshot.queue2.depthBatches).toBe(0)
    expect(
      snapshot.sender.workerStates.inFlight + snapshot.sender.workerStates.backoff,
    ).toBe(1)
    expect(snapshot.queue2.blockedSenders).toBe(1)
    expect(derived.queue2).toMatchObject({
      displayedPressure: 1,
      flowState: 'backpressure',
    })
    adapter.dispose()
  })

  it('freezes backoff time and counters exactly while paused', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'set-worker-count', actor: 'sender', value: 1 })
    await adapter.dispatch({ type: 'set-read-batch-size', value: 1_000 })
    await adapter.dispatch({ type: 'set-requested-tps', value: 50_000 })
    await adapter.dispatch({ type: 'set-target-delay', valueMs: 0 })
    await adapter.dispatch({ type: 'set-target-error-rate', valuePercent: 100 })
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().sender.workerStates.backoff).toBe(1)

    await adapter.dispatch({ type: 'pause' })
    const paused = adapter.getSnapshot()
    await vi.advanceTimersByTimeAsync(1_000)
    expect(adapter.getSnapshot()).toBe(paused)
    expect(paused.sender.retryAttemptsStartedTotal).toBe(0)

    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().sender.retryAttemptsStartedTotal).toBe(0)
    await vi.advanceTimersByTimeAsync(100)
    expect(adapter.getSnapshot().sender.retryAttemptsStartedTotal).toBe(1)
    adapter.dispose()
  })

  it('classifies network no-response through the same retry state machine', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(
      DIRECT_CONFIG,
      4,
      10,
      (context) => {
        attempts.push(context)
        return context.attempt === 1
          ? { kind: 'network-error', latencyMs: 10 }
          : { kind: 'http-response', statusCode: 200, latencyMs: 10 }
      },
    )
    advanceUntil(
      simulation,
      () => simulation.telemetry(true).http.requestsSucceededTotal === 1,
    )
    const snapshot = simulation.telemetry(true)

    expect(attempts.slice(0, 2).map(({ attempt }) => attempt)).toEqual([1, 2])
    expect(snapshot.http.networkErrorsTotal).toBe(1)
    expect(snapshot.http.responsesRejectedTotal).toBe(0)
    expect(snapshot.sender.retryAttemptsStartedTotal).toBe(1)
    expect(snapshot.sender.duplicateRiskTransactionsTotal).toBe(1_000)
    expect(snapshot.sender.terminalFailedBatchesTotal).toBe(0)
  })

  it('rejects reset while running without changing runtime identity', async () => {
    const adapter = new SimulationAdapter()
    await adapter.dispatch({ type: 'run' })
    await vi.advanceTimersByTimeAsync(200)
    const before = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'reset' })

    expect(receipt).toMatchObject({
      accepted: false,
      error: { code: 'invalid-state', retryable: false },
    })
    expect(adapter.getSnapshot()).toBe(before)
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
