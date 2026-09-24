import { afterEach, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from './SimulationAdapter'

describe('SimulationAdapter', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('publishes an immutable Sender snapshot with retained controls', () => {
    const adapter = new SimulationAdapter()
    const snapshot = adapter.getSnapshot()

    expect(Object.isFrozen(snapshot)).toBe(true)
    expect(Object.isFrozen(snapshot.sender)).toBe(true)
    expect(snapshot.sender.workers.applied).not.toBeNull()
    expect(Object.keys(snapshot.sender)).toEqual(expect.arrayContaining([
      'workers',
      'timeoutMs',
      'workerSlots',
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
})
