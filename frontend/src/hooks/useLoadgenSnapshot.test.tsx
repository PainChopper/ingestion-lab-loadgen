import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenTelemetrySnapshot } from '../model/loadgen'
import { PauseFrozenObservedDeriver } from './useLoadgenSnapshot'

function runningSnapshot(): LoadgenTelemetrySnapshot {
  const adapter = new SimulationAdapter()
  const base = adapter.getSnapshot()
  adapter.dispose()
  return {
    ...base,
    revision: 1,
    runState: 'running',
    elapsedMs: 12_000,
    totalTransactions: 900,
    reader: {
      ...base.reader,
      state: 'running',
      liveWorkers: 2,
      workerSlots: [
        { id: 'reader-1', ordinal: 1, activity: 'reading', lifecycle: 'active', source: 'rows.csv' },
      ],
      readTps: 75,
      rowsRead: 900,
    },
    throttler: { ...base.throttler, state: 'running', admittedTps: 75 },
    readerChannel: { ...base.readerChannel, depthBatches: 3, inputTps: 75, outputTps: 70 },
    senderChannel: { ...base.senderChannel, depthBatches: 2, inputTps: 70, outputTps: 65 },
    sender: {
      ...base.sender,
      state: 'running',
      liveWorkers: 2,
      workerSlots: [
        { id: 'sender-1', ordinal: 1, activity: 'in-flight', lifecycle: 'active', terminalError: false },
      ],
      attemptedTps: 65,
      successfulResponses: 780,
    },
    http: { ...base.http, throughputTps: 65, latencyP95Ms: 42 },
    target: { ...base.target, acceptedTps: 65, latencyP95Ms: 40 },
  }
}

function pausedSnapshot(running: LoadgenTelemetrySnapshot): LoadgenTelemetrySnapshot {
  return {
    ...running,
    revision: 2,
    runState: 'paused',
    elapsedMs: 0,
    totalTransactions: 0,
    reader: {
      ...running.reader,
      state: 'paused',
      workers: { ...running.reader.workers, applied: 7 },
      liveWorkers: 0,
      workerSlots: [],
      readTps: 0,
      rowsRead: 0,
    },
    throttler: {
      ...running.throttler,
      state: 'paused',
      requestedTps: { ...running.throttler.requestedTps, applied: 77 },
      admittedTps: 0,
    },
    readerChannel: { ...running.readerChannel, depthBatches: 0, inputTps: 0, outputTps: 0 },
    senderChannel: { ...running.senderChannel, depthBatches: 0, inputTps: 0, outputTps: 0 },
    sender: {
      ...running.sender,
      state: 'paused',
      workers: { ...running.sender.workers, applied: 9 },
      liveWorkers: 0,
      workerSlots: [],
      attemptedTps: 0,
      successfulResponses: 0,
    },
    http: { ...running.http, throughputTps: 0, latencyP95Ms: null },
    target: { ...running.target, acceptedTps: 0, latencyP95Ms: null },
  }
}

describe('PauseFrozenObservedDeriver', () => {
  it('captures only the first preceding running observation and keeps controls live', () => {
    const deriver = new PauseFrozenObservedDeriver()
    const running = runningSnapshot()
    const paused = pausedSnapshot(running)

    deriver.derive(running)
    const firstPaused = deriver.derive(paused)
    const repeatedPaused = deriver.derive({
      ...paused,
      revision: 3,
      sender: { ...paused.sender, attemptedTps: 1, workerSlots: [] },
    })

    expect(firstPaused.frozenObserved).toBe(true)
    expect(firstPaused.snapshot.totalTransactions).toBe(900)
    expect(firstPaused.snapshot.reader.workerSlots).toEqual(running.reader.workerSlots)
    expect(firstPaused.snapshot.sender.attemptedTps).toBe(65)
    expect(firstPaused.snapshot.sender.workers.applied).toBe(9)
    expect(firstPaused.snapshot.throttler.requestedTps.applied).toBe(77)
    expect(repeatedPaused.snapshot.sender.attemptedTps).toBe(65)
    expect(repeatedPaused.snapshot.sender.workerSlots).toEqual(running.sender.workerSlots)
  })

  it('clears the frozen observation for the first fresh running snapshot and reset idle', () => {
    const deriver = new PauseFrozenObservedDeriver()
    const running = runningSnapshot()
    const paused = pausedSnapshot(running)

    deriver.derive(running)
    deriver.derive(paused)
    const resumed = deriver.derive({ ...running, revision: 3, totalTransactions: 1_200 })
    expect(resumed.frozenObserved).toBe(false)
    expect(resumed.snapshot.totalTransactions).toBe(1_200)

    deriver.derive(paused)
    const reset = deriver.derive({ ...paused, revision: 4, runState: 'idle' })
    expect(reset.frozenObserved).toBe(false)
    expect(reset.snapshot.runState).toBe('idle')
    expect(reset.snapshot.totalTransactions).toBe(0)
  })

  it('does not invent history for cold pause and preserves live connection error precedence', () => {
    const running = runningSnapshot()
    const cold = pausedSnapshot(running)
    const coldDeriver = new PauseFrozenObservedDeriver()
    const coldPaused = coldDeriver.derive(cold)
    expect(coldPaused.frozenObserved).toBe(false)
    expect(coldPaused.snapshot.sender.workerSlots).toEqual([])

    const deriver = new PauseFrozenObservedDeriver()
    deriver.derive(running)
    const error = deriver.derive({
      ...cold,
      connectionState: 'error',
      http: { ...cold.http, connectionState: 'error' },
      target: { ...cold.target, connectionState: 'error' },
    })
    expect(error.frozenObserved).toBe(true)
    expect(error.snapshot.connectionState).toBe('error')
    expect(error.snapshot.http.connectionState).toBe('error')
    expect(error.snapshot.sender.attemptedTps).toBe(65)
  })
})
