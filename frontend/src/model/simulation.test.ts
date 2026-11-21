import { describe, expect, it } from 'vitest'
import {
  FixedStepSimulation,
  type SimulationAttemptContext,
  type SimulationAttemptOutcome,
  type SimulationConfig,
} from './simulation'

const CONFIG: SimulationConfig = {
  readerWorkers: 1,
  senderWorkers: 1,
  requestedTps: 50_000,
  throttlerInstalled: true,
  readBatchSize: 5_000,
  httpTimeoutMs: 500,
  targetDelayMs: 0,
  targetErrorRatePercent: 0,
}

describe('FixedStepSimulation pipeline batch', () => {
  it.each([0, 2])('reuses the first idle Sender at capacity %i', (capacity) => {
    const simulation = new FixedStepSimulation(
      { ...CONFIG, senderWorkers: 2, readBatchSize: 500 },
      0,
      capacity,
      () => ({ kind: 'http-response', statusCode: 200, latencyMs: 0 }),
    )

    simulation.advanceStep()
    simulation.advanceStep()

    expect(simulation.telemetry().sender.workerStates).toEqual([
      { activity: 'inFlight', draining: false },
      { activity: 'idle', draining: false },
    ])
    expect(simulation.telemetry().senderChannel.outputTransactionsTotal).toBe(1_000)
  })

  it.each([0, 2])('waits for an active Sender at capacity %i during scale-down', (capacity) => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(
      {
        ...CONFIG,
        readerWorkers: 4,
        senderWorkers: 4,
        readBatchSize: 500,
        throttlerInstalled: false,
      },
      0,
      capacity,
      (context) => {
        attempts.push(context)
        return {
          kind: 'http-response',
          statusCode: 200,
          latencyMs: context.batch.sequence === 0 ? 30 : context.batch.sequence === 1 ? 100 : 0,
        }
      },
    )

    simulation.advanceStep()
    simulation.updateConfig({ readerWorkers: 1, senderWorkers: 1 })
    simulation.advanceStep()
    const waiting = simulation.telemetry()

    expect(waiting.sender.workerStates).toEqual([
      { activity: 'inFlight', draining: false },
      { activity: 'inFlight', draining: true },
      { activity: 'idle', draining: true },
      { activity: 'idle', draining: true },
    ])
    expect(attempts).toHaveLength(4)
    expect(waiting.senderChannel.outputTransactionsTotal).toBe(2_000)
    expect(waiting.senderChannel.depthBatches).toBe(capacity === 0 ? 0 : 1)

    simulation.advanceStep()
    expect(attempts).toHaveLength(4)
    expect(simulation.telemetry().senderChannel.outputTransactionsTotal).toBe(2_000)
    simulation.advanceStep()
    expect(attempts[4].batch.sequence).toBe(4)
    expect(simulation.telemetry().senderChannel.outputTransactionsTotal).toBe(2_500)
  })

  it.each([
    { name: 'success', kind: 'http-response', statusCode: 204, confirmed: 5_000 },
    { name: 'permanent rejection', kind: 'http-response', statusCode: 400, confirmed: 0 },
    { name: 'exhausted retry', kind: 'http-response', statusCode: 503, confirmed: 0 },
    { name: 'timeout', kind: 'timeout', statusCode: 0, confirmed: 0 },
    { name: 'network failure', kind: 'network-error', statusCode: 0, confirmed: 0 },
    { name: 'successful retry', kind: 'http-response', statusCode: 503, confirmed: 5_000 },
  ] as const)('counts confirmations only after $name, without recounting retries', (scenario) => {
    const simulation = new FixedStepSimulation(
      CONFIG,
      0,
      0,
      ({ attempt }) =>
        ({
          kind: scenario.kind,
          statusCode:
            scenario.name === 'successful retry' && attempt > 1 ? 204 : scenario.statusCode,
          latencyMs: 10,
        }) as SimulationAttemptOutcome,
    )
    for (let step = 0; step < 10; step += 1) simulation.advanceStep()
    const inFlight = simulation.telemetry()
    expect(inFlight.run.totalTransactions).toBe(0)
    expect(inFlight.senderChannel.inputTransactionsTotal).toBe(5_000)
    simulation.updateConfig({ requestedTps: 0 })
    for (let step = 0; step < 200; step += 1) simulation.advanceStep()
    const completed = simulation.telemetry()
    expect(completed.run.totalTransactions).toBe(scenario.confirmed)
    expect(completed.reader.rowsRead).toBe(5_000)
    for (const channel of [completed.readerChannel, completed.senderChannel]) {
      expect(channel.inputTransactionsTotal).toBe(5_000)
      expect(channel.outputTransactionsTotal).toBe(5_000)
      expect(channel.bufferedTransactions).toBe(0)
    }
    expect(completed.readerChannel.blockedMs).toBeGreaterThan(inFlight.readerChannel.blockedMs)
    expect(completed.capturedAtMs).toBe(completed.run.elapsedMs)
  })

  it.each([0, 2])(
    'bypasses the token bucket and preserves backpressure at capacity %i',
    (capacity) => {
      const simulation = new FixedStepSimulation(
        { ...CONFIG, readerWorkers: 5, requestedTps: 0, targetDelayMs: 100 },
        capacity,
        capacity,
      )

      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      const installed = simulation.telemetry()
      expect(installed.senderChannel.inputTransactionsTotal).toBe(0)
      expect(installed.readerChannel.blockedSenders).toBeGreaterThan(0)

      simulation.updateConfig({ throttlerInstalled: false })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      const bypassed = simulation.telemetry()
      expect(bypassed.senderChannel.inputTransactionsTotal).toBeGreaterThan(0)
      expect(bypassed.senderChannel.blockedSenders).toBeGreaterThan(0)
      expect(bypassed.senderChannel.oldestBlockedSenderMs).toBeGreaterThan(0)
      expect(bypassed.senderChannel.depthBatches).toBe(capacity)
      expect(bypassed.readerChannel.outputTransactionsTotal).toBeGreaterThan(0)
      expect(bypassed.senderChannel.outputTransactionsTotal).toBeGreaterThan(0)
      if (capacity === 0) expect(bypassed.readerChannel.depthBatches).toBe(0)

      simulation.updateConfig({ throttlerInstalled: true })
      for (let step = 0; step < 100; step += 1) simulation.advanceStep()
      const reinstalled = simulation.telemetry()
      expect(simulation.config.requestedTps).toBe(0)
      expect(reinstalled.run.totalTransactions).toBeGreaterThanOrEqual(
        bypassed.run.totalTransactions,
      )
      expect(reinstalled.senderChannel.inputTransactionsTotal).toBe(
        bypassed.senderChannel.inputTransactionsTotal,
      )
      expect(reinstalled.readerChannel.blockedMs).toBeGreaterThan(bypassed.readerChannel.blockedMs)
      expect(reinstalled.readerChannel.blockedSenders).toBeGreaterThan(0)

      simulation.updateConfig({ requestedTps: CONFIG.requestedTps })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      expect(simulation.telemetry().senderChannel.inputTransactionsTotal).toBeGreaterThan(
        reinstalled.senderChannel.inputTransactionsTotal,
      )
      expect(simulation.telemetry().run.totalTransactions).toBeGreaterThan(
        reinstalled.run.totalTransactions,
      )
    },
  )

  it('forwards each Reader batch unchanged through both channels', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(CONFIG, 0, 0, (context) => {
      attempts.push(context)
      return { kind: 'http-response', statusCode: 200, latencyMs: 0 }
    })

    for (let step = 0; step < 200; step += 1) simulation.advanceStep()
    simulation.updateConfig({ requestedTps: 0 })
    simulation.advanceStep()
    const telemetry = simulation.telemetry()

    expect(attempts.length).toBeGreaterThan(0)
    expect(
      attempts.every(
        ({ batch }) =>
          batch.transactions === CONFIG.readBatchSize &&
          batch.identity === `pipeline-batch-${batch.sequence}`,
      ),
    ).toBe(true)
    expect(telemetry.run.totalTransactions).toBe(attempts.length * CONFIG.readBatchSize)
    expect(telemetry.reader.rowsRead).toBe(telemetry.run.totalTransactions)
    expect(telemetry.readerChannel.depthBatches).toBe(0)
    expect(telemetry.senderChannel.depthBatches).toBe(0)
  })

  it('keeps a scale-down worker in the draining aggregate while it is in flight', () => {
    const simulation = new FixedStepSimulation(
      {
        ...CONFIG,
        senderWorkers: 2,
        readBatchSize: 1_000,
        targetDelayMs: 100,
      },
      4,
      4,
    )

    for (let step = 0; step < 50; step += 1) {
      simulation.advanceStep()
      if (
        simulation.telemetry().sender.workerStates.filter(({ activity }) => activity === 'inFlight')
          .length === 2
      )
        break
    }

    expect(
      simulation.telemetry().sender.workerStates.filter(({ activity }) => activity === 'inFlight')
        .length,
    ).toBe(2)

    simulation.updateConfig({ senderWorkers: 1 })

    const telemetry = simulation.telemetry().sender
    expect(telemetry.workerStates).toHaveLength(2)
    expect(telemetry.workerStates).toEqual(
      expect.arrayContaining([
        { activity: 'inFlight', draining: false },
        { activity: 'inFlight', draining: true },
      ]),
    )
  })

  it('retains batches across backoff and drains retiring workers after retry', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(
      { ...CONFIG, senderWorkers: 2, readBatchSize: 1_000 },
      4,
      4,
      (context) => {
        attempts.push(context)
        return {
          kind: 'http-response',
          statusCode: context.attempt === 1 ? 503 : 200,
          latencyMs: 0,
        }
      },
    )
    for (let step = 0; step < 50; step += 1) {
      simulation.advanceStep()
      if (
        simulation.telemetry().sender.workerStates.filter(({ activity }) => activity === 'backoff')
          .length === 2
      )
        break
    }
    expect(
      simulation.telemetry().sender.workerStates.filter(({ activity }) => activity === 'backoff')
        .length,
    ).toBe(2)
    simulation.updateConfig({ senderWorkers: 1, requestedTps: 0 })
    const beforeRetry = simulation.telemetry().sender.workerStates
    expect(beforeRetry).toHaveLength(2)
    expect(beforeRetry).toEqual(
      expect.arrayContaining([
        { activity: 'backoff', draining: false },
        { activity: 'backoff', draining: true },
      ]),
    )
    for (let step = 0; step < 50; step += 1) simulation.advanceStep()
    expect(simulation.telemetry().sender.workerStates).toEqual([
      { activity: 'idle', draining: false },
    ])
    expect(beforeRetry).toHaveLength(2)
    expect(beforeRetry).toEqual(
      expect.arrayContaining([
        { activity: 'backoff', draining: false },
        { activity: 'backoff', draining: true },
      ]),
    )
    expect(Object.isFrozen(beforeRetry)).toBe(true)
    expect(beforeRetry.every(Object.isFrozen)).toBe(true)
    for (const first of attempts.filter(({ attempt }) => attempt === 1)) {
      const retry = attempts.find(({ batch, attempt }) => batch === first.batch && attempt === 2)
      expect(retry?.batch).toBe(first.batch)
      expect(retry!.startedAtMs).toBeGreaterThan(first.startedAtMs)
    }
  })
})
