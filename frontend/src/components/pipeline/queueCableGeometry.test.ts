import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  buildQueueCablePath,
  cableYToCapacity,
  capacityFromKeyboard,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getQueueCapacityPresentation,
  getQueueCableGeometryPresentation,
  getQueueMarkerCount,
  normalizeCapacity,
} from './queueCableGeometry'

const smallRange = { min: 0, max: 12, step: 1 }
const largeRange = { min: 0, max: 160, step: 10 }

function capacityControl(
  applied: number,
  range = smallRange,
  preview: number | null = null,
  pending: number | null = preview,
): NumericControlSnapshot {
  return {
    applied,
    preview,
    pending,
    ...range,
    unit: 'batches',
    applyMode: 'immediate',
  }
}

describe('queue cable capacity geometry', () => {
  it('clamps and snaps values on both configured scales', () => {
    expect([
      normalizeCapacity(-4, smallRange),
      normalizeCapacity(5.6, smallRange),
      normalizeCapacity(147, largeRange),
      normalizeCapacity(999, largeRange),
    ]).toEqual([0, 6, 150, 160])
  })

  it('maps both capacity scales to height and back', () => {
    const cases = [
      { range: smallRange, capacity: 6, y: 295 },
      { range: largeRange, capacity: 80, y: 295 },
    ]

    for (const testCase of cases) {
      expect(capacityToCableY(testCase.capacity, testCase.range, 415, 240))
        .toBe(testCase.y)
      expect(cableYToCapacity(testCase.y, testCase.range, 415, 240))
        .toBe(testCase.capacity)
      expect(cableYToCapacity(100, testCase.range, 415, 240))
        .toBe(testCase.range.max)
      expect(cableYToCapacity(500, testCase.range, 415, 240))
        .toBe(testCase.range.min)
    }
  })

  it('normalizes vertical drag from its current candidate and clamps bounds', () => {
    expect(capacityFromVerticalDrag(6, 0, smallRange, 240)).toBe(6)
    expect(capacityFromVerticalDrag(6, 9, smallRange, 240)).toBe(6)
    expect(capacityFromVerticalDrag(6, -20, smallRange, 240)).toBe(7)
    expect(capacityFromVerticalDrag(6, -500, smallRange, 240)).toBe(12)
    expect(capacityFromVerticalDrag(80, 15, largeRange, 240)).toBe(70)
    expect(capacityFromVerticalDrag(80, 500, largeRange, 240)).toBe(0)
  })

  it('maps keyboard commands from pending values on both scales', () => {
    const cases = [
      {
        range: smallRange,
        current: 6,
        expected: [7, 5, 11, 1, 0, 12],
      },
      {
        range: largeRange,
        current: 20,
        expected: [30, 10, 70, 0, 0, 160],
      },
    ]
    const keys = ['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End']

    for (const testCase of cases) {
      expect(keys.map((key) =>
        capacityFromKeyboard(key, testCase.current, testCase.range),
      )).toEqual(testCase.expected)
      expect(capacityFromKeyboard('Escape', testCase.current, testCase.range))
        .toBeNull()
    }
  })

  it('distinguishes applied, pending, and local preview capacity', () => {
    const cases = [
      {
        control: capacityControl(4),
        localPreview: null,
        expected: { applied: 4, candidate: 4, requestState: null },
      },
      {
        control: capacityControl(12, smallRange, 4, 4),
        localPreview: null,
        expected: { applied: 12, candidate: 4, requestState: 'pending' },
      },
      {
        control: capacityControl(12, smallRange, 4, 4),
        localPreview: 7,
        expected: { applied: 12, candidate: 7, requestState: 'preview' },
      },
      {
        control: capacityControl(0),
        localPreview: null,
        expected: { applied: 0, candidate: 0, requestState: null },
      },
    ] as const

    for (const testCase of cases) {
      expect(getQueueCapacityPresentation(
        testCase.control,
        testCase.localPreview,
      )).toEqual(testCase.expected)
    }
  })

  it('keeps applied cable and travel length stable while candidates move', () => {
    const cases = [
      {
        control: capacityControl(12, smallRange, 5, 5),
        start: { x: 150, y: 415 },
        end: { x: 355, y: 415 },
        appliedY: 175,
        candidateY: 315,
      },
      {
        control: capacityControl(100, largeRange, 30, 30),
        start: { x: 505, y: 415 },
        end: { x: 720, y: 415 },
        appliedY: 265,
        candidateY: 370,
      },
    ]

    for (const testCase of cases) {
      const pending = getQueueCableGeometryPresentation(
        testCase.control,
        testCase.start,
        testCase.end,
      )
      const preview = getQueueCableGeometryPresentation(
        testCase.control,
        testCase.start,
        testCase.end,
        testCase.control.pending,
      )
      const canonical = getQueueCableGeometryPresentation(
        { ...testCase.control, preview: null, pending: null },
        testCase.start,
        testCase.end,
      )

      expect(preview.cablePath).toBe(canonical.cablePath)
      expect(preview.markerPath).toBe(canonical.markerPath)
      expect(pending.cableY).toBe(testCase.candidateY)
      expect(pending.sliderY).toBe(testCase.candidateY)
      expect(pending.requestedPath).toBeNull()
      expect(preview.sliderY).toBe(testCase.candidateY)
      expect(preview.requestedPath).toBe(
        buildQueueCablePath(testCase.start, testCase.end, testCase.candidateY),
      )
    }
  })

  it('builds flat zero-capacity and continuous lifted paths', () => {
    const start = { x: 150, y: 415 }
    const end = { x: 355, y: 415 }

    expect(buildQueueCablePath(start, end, 415)).toBe('M150 415 H355')
    expect(buildQueueCablePath(start, end, 295)).toMatch(
      /^M150 415 H.+ Q.+ V.+ Q.+ H.+ Q.+ V.+ Q.+ H355$/,
    )
  })

  it('exposes every 0..12 step 1 and 0..160 step 10 capacity tick', () => {
    const smallTicks = getCapacityTicks(smallRange, 415, 240)
    const largeTicks = getCapacityTicks(largeRange, 415, 240)

    expect(smallTicks.map((tick) => tick.value)).toEqual(
      Array.from({ length: 13 }, (_, index) => index),
    )
    expect(largeTicks.map((tick) => tick.value)).toEqual(
      Array.from({ length: 17 }, (_, index) => index * 10),
    )
    expect(smallTicks.filter((tick) => tick.major).map((tick) => tick.value))
      .toEqual([0, 6, 12])
    expect(largeTicks.filter((tick) => tick.major).map((tick) => tick.value))
      .toEqual([0, 80, 160])
  })

  it('bounds occupancy by depth, fill ratio, and the fixed marker pool', () => {
    expect([
      getQueueMarkerCount(4, 0),
      getQueueMarkerCount(4, 4),
      getQueueMarkerCount(4, 12),
      getQueueMarkerCount(15, 100),
      getQueueMarkerCount(50, 100),
      getQueueMarkerCount(100, 100),
    ]).toEqual([0, 4, 4, 4, 12, 24])
  })

  it('never increases occupancy marker count when capacity grows', () => {
    const capacities = [1, 2, 3, 4, 5, 8, 12, 16, 24, 50, 100, 250]

    for (const depth of [1, 4, 8, 15, 24, 50, 100]) {
      const targets = capacities.map((capacity) =>
        getQueueMarkerCount(depth, capacity),
      )
      expect(targets.every((target) => target <= depth && target <= 24))
        .toBe(true)
      expect(targets.every((target, index) =>
        index === 0 || target <= targets[index - 1],
      )).toBe(true)
    }
  })
})
