import { describe, expect, it } from 'vitest'
import {
  FixedStepSimulation,
  type SimulationAttemptContext,
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
      expect(installed.senderChannel.inputTransactionsPerSecond).toBe(0)
      expect(installed.readerChannel.blockedSenders).toBeGreaterThan(0)

      simulation.updateConfig({ throttlerInstalled: false })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      const bypassed = simulation.telemetry()
      expect(bypassed.senderChannel.inputTransactionsPerSecond).toBeGreaterThan(0)
      expect(bypassed.senderChannel.blockedSenders).toBeGreaterThan(0)
      expect(bypassed.senderChannel.oldestBlockedSenderMs).toBeGreaterThan(0)
      expect(bypassed.senderChannel.depthBatches).toBe(capacity)
      expect(bypassed.readerChannel.outputTransactionsPerSecond).toBeGreaterThan(0)
      expect(bypassed.senderChannel.outputTransactionsPerSecond).toBeGreaterThan(0)
      if (capacity === 0) expect(bypassed.readerChannel.depthBatches).toBe(0)

      simulation.updateConfig({ throttlerInstalled: true })
      for (let step = 0; step < 100; step += 1) simulation.advanceStep()
      const reinstalled = simulation.telemetry()
      expect(simulation.config.requestedTps).toBe(0)
      expect(reinstalled.totalTransactions).toBe(bypassed.totalTransactions)
      expect(reinstalled.senderChannel.inputTransactionsPerSecond).toBe(0)
      expect(reinstalled.readerChannel.blockedSenders).toBeGreaterThan(0)

      simulation.updateConfig({ requestedTps: CONFIG.requestedTps })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      expect(simulation.telemetry().senderChannel.inputTransactionsPerSecond).toBeGreaterThan(0)
      expect(simulation.telemetry().totalTransactions).toBeGreaterThan(
        reinstalled.totalTransactions,
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
    const telemetry = simulation.telemetry()

    expect(attempts.length).toBeGreaterThan(0)
    expect(
      attempts.every(
        ({ batch }) =>
          batch.transactions === CONFIG.readBatchSize &&
          batch.identity === `pipeline-batch-${batch.sequence}`,
      ),
    ).toBe(true)
    expect(telemetry.totalTransactions).toBe(attempts.length * CONFIG.readBatchSize)
    expect(telemetry.rowsRead).toBe(telemetry.totalTransactions)
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
