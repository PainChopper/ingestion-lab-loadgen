import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import { capacityToCableY, QUEUE_CABLE_MAX_LIFT } from './queueCableGeometry'
import { getQueueCablePresentation } from './QueueCable'

describe('QueueCable presentation', () => {
  it('keeps zero depth visible while showing the current upstream wait', () => {
    const adapter = new SimulationAdapter()
    const snapshot = adapter.getSnapshot()
    const queue = {
      ...snapshot.queue1,
      depthBatches: 0,
      blockedSenders: 1,
      oldestBlockedSenderMs: 450,
    }
    const telemetry = getQueueCablePresentation({
      ...queue,
      capacity: {
        ...queue.capacity,
        applied: 0,
        preview: null,
        pending: null,
      },
    })

    expect(telemetry.depth).toBe('Depth 0 / 0 batches')
    expect(telemetry.waitingUpstream).toBe(
      'Waiting upstream 1, oldest 450 ms',
    )
    expect(telemetry.waitingUpstreamY).toBe(548)
    expect(telemetry.capacityStatusY).toBe(566)
    adapter.dispose()
  })

  it('shares the compact status row when nobody is waiting upstream', () => {
    const adapter = new SimulationAdapter()
    const telemetry = getQueueCablePresentation(adapter.getSnapshot().queue1)

    expect(telemetry.waitingUpstream).toBeNull()
    expect(telemetry.capacityStatusY).toBe(548)
    adapter.dispose()
  })

  it('keeps the handle on applied 12 and marks pending 8 separately', () => {
    const adapter = new SimulationAdapter()
    const queue = adapter.getSnapshot().queue1
    const snapshot = {
      ...queue,
      depthBatches: 12,
      capacity: {
        ...queue.capacity,
        applied: 12,
        preview: 8,
        pending: 8,
      },
    }
    const presentation = getQueueCablePresentation(snapshot)

    expect(presentation.handleCapacity).toBe(12)
    expect(presentation.target).toEqual({ capacity: 8, state: 'pending' })
    expect(
      capacityToCableY(
        presentation.handleCapacity,
        snapshot.capacity,
        415,
        QUEUE_CABLE_MAX_LIFT,
      ),
    ).toBe(175)
    expect(
      capacityToCableY(
        presentation.target?.capacity ?? 0,
        snapshot.capacity,
        415,
        QUEUE_CABLE_MAX_LIFT,
      ),
    ).toBe(255)
    adapter.dispose()
  })

  it('moves the handle and hides the target marker during a local drag', () => {
    const adapter = new SimulationAdapter()
    const queue = adapter.getSnapshot().queue1
    const presentation = getQueueCablePresentation(
      {
        ...queue,
        capacity: {
          ...queue.capacity,
          applied: 12,
          preview: 8,
          pending: 8,
        },
      },
      7,
    )

    expect(presentation.handleCapacity).toBe(7)
    expect(presentation.handleState).toBe('preview')
    expect(presentation.target).toBeNull()
    adapter.dispose()
  })
})
