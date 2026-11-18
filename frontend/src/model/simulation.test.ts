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
      expect(installed.senderChannel.sentBatchesTotal).toBe(0)
      expect(installed.limitedMs).toBeGreaterThan(0)

      simulation.updateConfig({ throttlerInstalled: false })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      const bypassed = simulation.telemetry()
      expect(bypassed.senderChannel.sentBatchesTotal).toBeGreaterThan(0)
      expect(bypassed.senderChannel.blockedMsTotal).toBeGreaterThan(0)
      expect(bypassed.senderChannel.depthBatches).toBe(capacity)
      expect(bypassed.senderChannel.handoffBatchesTotal > 0).toBe(capacity === 0)
      expect(bypassed.readerChannel.handoffBatchesTotal > 0).toBe(capacity === 0)
      expect(bypassed.limitedMs).toBe(installed.limitedMs)
      expect(bypassed.readerChannel.receivedBatchesTotal).toBe(
        bypassed.senderChannel.sentBatchesTotal + 1,
      )

      simulation.updateConfig({ throttlerInstalled: true })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      const reinstalled = simulation.telemetry()
      expect(simulation.config.requestedTps).toBe(0)
      expect(reinstalled.readerChannel.receivedBatchesTotal).toBe(
        bypassed.readerChannel.receivedBatchesTotal,
      )
      expect(reinstalled.senderChannel.sentBatchesTotal).toBe(
        bypassed.senderChannel.sentBatchesTotal,
      )
      expect(reinstalled.limitedMs).toBeGreaterThan(bypassed.limitedMs)

      simulation.updateConfig({ requestedTps: CONFIG.requestedTps })
      for (let step = 0; step < 50; step += 1) simulation.advanceStep()
      expect(simulation.telemetry().senderChannel.sentBatchesTotal).toBeGreaterThan(
        reinstalled.senderChannel.sentBatchesTotal,
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
    expect(telemetry.senderChannel.sentBatchesTotal).toBe(
      telemetry.readerChannel.receivedBatchesTotal,
    )
    expect(telemetry.senderChannel.sentTransactionsTotal).toBe(
      telemetry.readerChannel.receivedTransactionsTotal,
    )
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
      if (simulation.telemetry().sender.workerStates.inFlight === 2) break
    }

    expect(simulation.telemetry().sender.workerStates.inFlight).toBe(2)

    simulation.updateConfig({ senderWorkers: 1 })

    const telemetry = simulation.telemetry().sender
    expect(telemetry.liveWorkers).toBe(2)
    expect(telemetry.workerStates.inFlight).toBe(1)
    expect(telemetry.drainingWorkerStates).toEqual({
      idle: 0,
      inFlight: 1,
      backoff: 0,
    })
    expect(simulation.telemetry().http.inFlightRequests).toBe(2)
  })
})
