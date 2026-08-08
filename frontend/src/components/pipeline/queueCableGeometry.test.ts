import { describe, expect, it } from 'vitest'
import {
  buildQueueCablePath,
  cableYToCapacity,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getQueueMarkerCount,
  normalizeCapacity,
} from './queueCableGeometry'

const smallRange = { min: 0, max: 12, step: 1 }
const largeRange = { min: 0, max: 160, step: 10 }

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

  it('shows one marker for active unbuffered handoff', () => {
    expect(getQueueMarkerCount(0, 0, 500, 'normal')).toBe(1)
  })

  it('shows no marker for an idle empty queue', () => {
    expect(getQueueMarkerCount(0, 0, 0, 'normal')).toBe(0)
  })

  it('bounds buffered occupancy markers by queue fill ratio', () => {
    expect(getQueueMarkerCount(1, 4, 0, 'normal')).toBe(3)
    expect(getQueueMarkerCount(3, 4, 0, 'normal')).toBe(9)
    expect(getQueueMarkerCount(100, 100, 0, 'normal')).toBe(12)
  })

  it('keeps queued markers when the queue is stopped', () => {
    expect(getQueueMarkerCount(3, 4, 0, 'stopped')).toBe(9)
    expect(getQueueMarkerCount(3, 0, 0, 'stopped')).toBe(12)
  })
})
