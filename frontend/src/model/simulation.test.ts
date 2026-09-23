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
  throttlerInstallationMode: 'installed',
  readBatchSize: 5_000,
  httpTimeoutMs: 500,
  targetDelayMs: 0,
  targetErrorRatePercent: 0,
}

describe('FixedStepSimulation pipeline batch', () => {
  it('forwards each Reader batch unchanged through both channels', () => {
    const attempts: SimulationAttemptContext[] = []
    const simulation = new FixedStepSimulation(
      CONFIG,
      0,
      0,
      (context) => {
        attempts.push(context)
        return { kind: 'http-response', statusCode: 200, latencyMs: 0 }
      },
    )

    for (let step = 0; step < 200; step += 1) simulation.advanceStep()
    const telemetry = simulation.telemetry(true)

    expect(attempts.length).toBeGreaterThan(0)
    expect(attempts.every(({ batch }) =>
      batch.transactions === CONFIG.readBatchSize &&
      batch.identity === `pipeline-batch-${batch.sequence}`,
    )).toBe(true)
    expect(telemetry.senderChannel.sentBatchesTotal).toBe(
      telemetry.readerChannel.receivedBatchesTotal,
    )
    expect(telemetry.senderChannel.sentTransactionsTotal).toBe(
      telemetry.readerChannel.receivedTransactionsTotal,
    )
  })
})
