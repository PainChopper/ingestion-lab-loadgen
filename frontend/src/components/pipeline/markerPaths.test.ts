import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import { getQueueMarkerPathGeometry } from './markerPaths'

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

  it('uses a distinct offset actor path and separates flow from handoff', () => {
    const geometry = getQueueMarkerPathGeometry(
      'throttler-to-sender',
      capacityControl(100),
    )

    expect(geometry.transientPath).not.toBe(geometry.occupancyPath)
    expect(geometry.transientPath).toContain('M432 394')
    expect(geometry.transientPath).toContain('L505 403')
    expect(geometry.flowEndRatio).toBeLessThan(geometry.handoffStartRatio)
    expect(geometry.flowLength).toBeGreaterThan(0)
    expect(geometry.handoffLength).toBeGreaterThan(0)
  })
})
