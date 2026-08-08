import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  buildQueueCablePath,
  cableYToCapacity,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getQueueCapacityPresentation,
  getQueueMarkerCount,
  normalizeCapacity,
} from './queueCableGeometry'

const smallRange = { min: 0, max: 12, step: 1 }
const largeRange = { min: 0, max: 160, step: 10 }

function capacityControl(
  applied: number,
  pending: number | null = null,
): NumericControlSnapshot {
  return {
    applied,
    preview: pending,
    pending,
    ...smallRange,
    unit: 'batches',
    applyMode: 'immediate',
  }
}

describe('queue cable capacity geometry', () => {
  it('clamps and snaps capacities to the configured range', () => {
    expect(normalizeCapacity(-4, smallRange)).toBe(0)
    expect(normalizeCapacity(5.6, smallRange)).toBe(6)
    expect(normalizeCapacity(147, largeRange)).toBe(150)
    expect(normalizeCapacity(999, largeRange)).toBe(160)
  })

  it('maps capacity and pointer height in both directions', () => {
    expect(capacityToCableY(0, smallRange, 415, 240)).toBe(415)
    expect(capacityToCableY(6, smallRange, 415, 240)).toBe(295)
    expect(capacityToCableY(12, smallRange, 415, 240)).toBe(175)
    expect(cableYToCapacity(295, smallRange, 415, 240)).toBe(6)
    expect(cableYToCapacity(100, smallRange, 415, 240)).toBe(12)
    expect(cableYToCapacity(500, smallRange, 415, 240)).toBe(0)
  })

  it('preserves capacity until a vertical drag changes its normalized value', () => {
    expect(capacityFromVerticalDrag(6, 0, smallRange, 240)).toBe(6)
    expect(capacityFromVerticalDrag(6, 9, smallRange, 240)).toBe(6)
    expect(capacityFromVerticalDrag(6, -20, smallRange, 240)).toBe(7)
  })

  it('clamps vertical drag results to the configured range', () => {
    expect(capacityFromVerticalDrag(6, -500, smallRange, 240)).toBe(12)
    expect(capacityFromVerticalDrag(6, 500, smallRange, 240)).toBe(0)
  })

  it.each([
    {
      name: 'keeps applied 4 while a decrease to 0 is pending at depth 4',
      control: capacityControl(4, 0),
      depth: 4,
      expectedApplied: 4,
      expectedCandidate: 0,
      expectedState: 'pending',
      expectedY: 335,
      expectedMarkers: 4,
    },
    {
      name: 'keeps applied 12 while a decrease to 4 is pending at depth 12',
      control: capacityControl(12, 4),
      depth: 12,
      expectedApplied: 12,
      expectedCandidate: 4,
      expectedState: 'pending',
      expectedY: 175,
      expectedMarkers: 12,
    },
    {
      name: 'shows an increase from 4 to 12 as immediately applied',
      control: capacityControl(12),
      depth: 4,
      expectedApplied: 12,
      expectedCandidate: 12,
      expectedState: null,
      expectedY: 175,
      expectedMarkers: 4,
    },
    {
      name: 'keeps applied zero as a flat unbuffered queue',
      control: capacityControl(0),
      depth: 0,
      expectedApplied: 0,
      expectedCandidate: 0,
      expectedState: null,
      expectedY: 415,
      expectedMarkers: 0,
    },
  ])('$name', ({
    control,
    depth,
    expectedApplied,
    expectedCandidate,
    expectedState,
    expectedY,
    expectedMarkers,
  }) => {
    const presentation = getQueueCapacityPresentation(control)

    expect(presentation).toEqual({
      applied: expectedApplied,
      candidate: expectedCandidate,
      requestState: expectedState,
    })
    expect(capacityToCableY(presentation.applied, control, 415, 240)).toBe(
      expectedY,
    )
    expect(
      getQueueMarkerCount(depth, presentation.applied),
    ).toBe(expectedMarkers)
  })

  it('keeps a local drag as preview while preserving applied geometry', () => {
    expect(getQueueCapacityPresentation(capacityControl(4), 0)).toEqual({
      applied: 4,
      candidate: 0,
      requestState: 'preview',
    })
  })

  it('keeps zero capacity flat and non-zero capacity continuous', () => {
    const start = { x: 150, y: 415 }
    const end = { x: 355, y: 415 }

    expect(buildQueueCablePath(start, end, 415)).toBe('M150 415 H355')
    expect(buildQueueCablePath(start, end, 295)).toMatch(
      /^M150 415 H.+ Q.+ V.+ Q.+ H.+ Q.+ V.+ Q.+ H355$/,
    )
  })

  it('builds readable major ticks for both queue ranges', () => {
    const smallTicks = getCapacityTicks(smallRange, 415, 240)
    const largeTicks = getCapacityTicks(largeRange, 415, 240)

    expect(smallTicks.filter((tick) => tick.major).map((tick) => tick.value)).toEqual([
      0, 6, 12,
    ])
    expect(largeTicks.filter((tick) => tick.major).map((tick) => tick.value)).toEqual([
      0, 80, 160,
    ])
    expect(largeTicks).toHaveLength(17)
  })

  it('keeps unbuffered occupancy empty because handoff markers are separate', () => {
    expect(getQueueMarkerCount(4, 0)).toBe(0)
    expect(getQueueMarkerCount(0, 0)).toBe(0)
  })

  it('bounds buffered occupancy markers by queue fill ratio', () => {
    expect(getQueueMarkerCount(4, 4)).toBe(4)
    expect(getQueueMarkerCount(4, 12)).toBe(4)
    expect(getQueueMarkerCount(4, 100)).toBe(1)
    expect(getQueueMarkerCount(15, 100)).toBe(4)
    expect(getQueueMarkerCount(50, 100)).toBe(12)
    expect(getQueueMarkerCount(100, 100)).toBe(24)
  })

  it('never increases occupancy when capacity grows at fixed depth', () => {
    const capacities = [1, 2, 3, 4, 5, 8, 12, 16, 24, 50, 100, 250]

    for (const depth of [1, 4, 8, 15, 24, 50, 100]) {
      const targets = capacities.map((capacity) =>
        getQueueMarkerCount(depth, capacity),
      )

      for (const [index, target] of targets.entries()) {
        expect(target).toBeLessThanOrEqual(depth)
        expect(target).toBeLessThanOrEqual(24)
        if (index > 0) {
          expect(target).toBeLessThanOrEqual(targets[index - 1])
        }
      }
    }
  })
})
