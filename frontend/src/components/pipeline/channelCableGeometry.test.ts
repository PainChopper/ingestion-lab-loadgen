import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  buildChannelCablePath,
  buildPortraitChannelCablePath,
  cableYToCapacity,
  capacityFromKeyboard,
  capacityFromVerticalDrag,
  capacityToCableY,
  getCapacityTicks,
  getChannelCapacityPresentation,
  getChannelCableGeometryPresentation,
  normalizeCapacity,
} from './channelCableGeometry'

const smallRange = { min: 0, max: 12, step: 1 }
const largeRange = { min: 0, max: 160, step: 10 }
const readerChannelRange = { min: 0, max: 8_192, step: 1 }
const policyAllowed = [0, 1, 2, 8, 64, 512, 8_192] as const

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

describe('channel cable capacity geometry', () => {
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

  it('uses equal index intervals for the readerChannel capacity scale', () => {
    const ticks = getCapacityTicks(
      readerChannelRange,
      415,
      240,
      policyAllowed,
    )

    expect(ticks.map((tick) => tick.value)).toEqual(policyAllowed)
    ticks.forEach((tick, index) => {
      expect(tick.y).toBeCloseTo(415 - index * 240 / 6, 12)
    })
    expect(capacityToCableY(
      8_192,
      readerChannelRange,
      415,
      240,
      policyAllowed,
    )).toBe(175)
    expect(cableYToCapacity(
      415 - 3 * 240 / 6,
      readerChannelRange,
      415,
      240,
      policyAllowed,
    )).toBe(8)
    expect(capacityFromVerticalDrag(
      2,
      -2 * 240 / 6,
      readerChannelRange,
      240,
      policyAllowed,
    )).toBe(64)
    expect(['ArrowUp', 'PageUp', 'PageDown', 'Home', 'End'].map((key) =>
      capacityFromKeyboard(
        key,
        2,
        readerChannelRange,
        policyAllowed,
      ),
    )).toEqual([8, 8_192, 0, 0, 8_192])
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
      expect(getChannelCapacityPresentation(
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
      const pending = getChannelCableGeometryPresentation(
        testCase.control,
        testCase.start,
        testCase.end,
      )
      const preview = getChannelCableGeometryPresentation(
        testCase.control,
        testCase.start,
        testCase.end,
        testCase.control.pending,
      )
      const canonical = getChannelCableGeometryPresentation(
        { ...testCase.control, preview: null, pending: null },
        testCase.start,
        testCase.end,
      )

      expect(preview.cablePath).toBe(canonical.cablePath)
      expect(pending.cableY).toBe(testCase.candidateY)
      expect(pending.sliderY).toBe(testCase.candidateY)
      expect(pending.requestedPath).toBeNull()
      expect(preview.sliderY).toBe(testCase.candidateY)
      expect(preview.requestedPath).toBe(
        buildChannelCablePath(testCase.start, testCase.end, testCase.candidateY),
      )
    }
  })

  it('builds flat zero-capacity and continuous lifted paths', () => {
    const start = { x: 150, y: 415 }
    const end = { x: 355, y: 415 }

    expect(buildChannelCablePath(start, end, 415)).toBe('M150 415 H355')
    expect(buildChannelCablePath(start, end, 295)).toMatch(
      /^M150 415 H.+ Q.+ V.+ Q.+ H.+ Q.+ V.+ Q.+ H355$/,
    )
  })

  it('builds portrait channels from exact vertical endpoints without overflow', () => {
    const start = { x: 240, y: 268 }
    const end = { x: 240, y: 515 }
    const zero = getChannelCableGeometryPresentation(
      capacityControl(0),
      start,
      end,
      null,
      'portrait',
    )
    const full = getChannelCableGeometryPresentation(
      capacityControl(12),
      start,
      end,
      null,
      'portrait',
    )

    expect(zero.cablePath).toBe('M240 268 V515')
    expect(full.cablePath).toBe(
      buildPortraitChannelCablePath(start, end, 100),
    )
    expect(full.sliderX).toBe(100)
    expect(full.sliderY).toBe(391.5)
    expect(full.cablePath.startsWith('M240 268')).toBe(true)
    expect(full.cablePath.endsWith('V515')).toBe(true)
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
})
