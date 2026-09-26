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

  it('keeps draining in-flight workers in aggregate and HTTP telemetry', async () => {
    vi.useFakeTimers()
    const adapter = new SimulationAdapter()

    await adapter.dispatch({ type: 'set-sender-workers', value: 2 })
    await adapter.dispatch({ type: 'set-throttler-installation-mode', value: 'bypass' })
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
    adapter.dispose()
  })
})
