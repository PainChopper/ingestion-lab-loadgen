import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server.browser'
import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import {
  buildQueueCablePath,
  capacityFromVerticalDrag,
  capacityToCableY,
  QUEUE_CABLE_MAX_LIFT,
} from './queueCableGeometry'
import { getQueueCablePresentation, QueueCable } from './QueueCable'

function elementWithClass(
  markup: string,
  tagName: 'g' | 'path',
  className: string,
): string {
  const element = markup.match(
    new RegExp(`<${tagName}[^>]*class="${className}(?: [^"]*)?"[^>]*>`),
  )?.[0]

  expect(element).toBeDefined()
  return element ?? ''
}

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

  it('keeps the handle and drag origin on pending 6 while applied stays 12', () => {
    const adapter = new SimulationAdapter()
    const queue = adapter.getSnapshot().queue1
    const snapshot = {
      ...queue,
      depthBatches: 12,
      capacity: {
        ...queue.capacity,
        applied: 12,
        preview: 6,
        pending: 6,
      },
    }
    const presentation = getQueueCablePresentation(snapshot)

    expect(presentation.handleCapacity).toBe(6)
    expect(presentation.handleState).toBe('pending')
    expect(presentation.dragStartCapacity).toBe(6)
    expect(presentation.appliedMarker).toEqual({ capacity: 12 })
    expect(
      capacityToCableY(
        presentation.appliedMarker?.capacity ?? 0,
        snapshot.capacity,
        415,
        QUEUE_CABLE_MAX_LIFT,
      ),
    ).toBe(175)
    expect(
      capacityToCableY(
        presentation.handleCapacity,
        snapshot.capacity,
        415,
        QUEUE_CABLE_MAX_LIFT,
      ),
    ).toBe(295)
    expect(
      capacityFromVerticalDrag(
        presentation.dragStartCapacity,
        -20,
        snapshot.capacity,
        QUEUE_CABLE_MAX_LIFT,
      ),
    ).toBe(7)
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
    expect(presentation.appliedMarker).toEqual({ capacity: 12 })
    adapter.dispose()
  })

  it.each([
    {
      queueKey: 'queue1',
      applied: 12,
      pending: 6,
      appliedY: 175,
      pendingY: 295,
    },
    {
      queueKey: 'queue2',
      applied: 160,
      pending: 20,
      appliedY: 175,
      pendingY: 385,
    },
  ] as const)(
    'renders $queueKey cable at applied $applied and slider at pending $pending',
    ({ queueKey, applied, pending, appliedY, pendingY }) => {
      const adapter = new SimulationAdapter()
      const queue = adapter.getSnapshot()[queueKey]
      const snapshot = {
        ...queue,
        depthBatches: applied,
        capacity: {
          ...queue.capacity,
          applied,
          preview: pending,
          pending,
        },
      }
      const start = { x: 150, y: 415 }
      const end = { x: 355, y: 415 }
      const presentation = getQueueCablePresentation(snapshot)
      const markup = renderToStaticMarkup(
        createElement(QueueCable, {
          snapshot,
          start,
          end,
          selected: false,
          markers: [],
          onSelect: () => undefined,
          onCapacityChange: () => undefined,
        }),
      )

      const cable = elementWithClass(markup, 'path', 'pipeline-queue-cable')
      const slider = elementWithClass(markup, 'g', 'pipeline-queue-handle')
      const appliedMarker = elementWithClass(
        markup,
        'g',
        'pipeline-queue-capacity-applied',
      )

      expect(cable).toContain(
        `d="${buildQueueCablePath(start, end, appliedY)}"`,
      )
      expect(slider).toContain('role="slider"')
      expect(slider).toContain(`aria-valuenow="${pending}"`)
      expect(slider).toContain(`transform="translate(252.5 ${pendingY})"`)
      expect(presentation.dragStartCapacity).toBe(pending)
      expect(appliedMarker).toContain(
        `transform="translate(202.5 ${appliedY})"`,
      )
      expect(markup).toContain(`>Applied ${applied}</text>`)
      expect(markup).not.toContain('pipeline-queue-capacity-target')
      adapter.dispose()
    },
  )
})
