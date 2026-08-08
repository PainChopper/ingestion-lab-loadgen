import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  FLOW_HANDLE_GAP,
  FLOW_MARKER_RADIUS,
  getQueueMarkerPathGeometry,
  QUEUE_HANDLE_HALF_HEIGHT,
} from './markerPaths'

function capacityControl(applied: number): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 0,
    max: 160,
    step: 10,
    unit: 'batches',
    applyMode: 'immediate',
  }
}

describe('marker paths', () => {
  it('keeps occupancy on the bounded cable between actor ports', () => {
    const geometry = getQueueMarkerPathGeometry(
      'reader-to-throttler',
      capacityControl(0),
    )

    expect(geometry.occupancyPath).toBe('M150 415 H355')
    expect(geometry.occupancyPath).not.toContain('96 392')
    expect(geometry.occupancyPath).not.toContain('384 394')
  })

  it('uses one actor-to-actor flow path across the full raised cable', () => {
    const geometry = getQueueMarkerPathGeometry(
      'throttler-to-sender',
      capacityControl(100),
    )

    expect(geometry.flowPath).not.toBe(geometry.occupancyPath)
    expect(geometry.flowPath).toContain('M432 394')
    expect(geometry.flowPath).toContain('L790 394')
    expect(geometry.flowPath.match(/V/g)).toHaveLength(2)
    expect(geometry.flowLength).toBeGreaterThan(0)
  })

  it('keeps the flow marker visibly clear of the capacity handle center', () => {
    const geometry = getQueueMarkerPathGeometry(
      'reader-to-throttler',
      {
        ...capacityControl(40),
        pending: 120,
      },
    )
    const markerBottomY = geometry.flowTopY + FLOW_MARKER_RADIUS
    const handleTopY = geometry.handleCenterY - QUEUE_HANDLE_HALF_HEIGHT

    expect(handleTopY - markerBottomY).toBeGreaterThanOrEqual(FLOW_HANDLE_GAP)
  })
})
