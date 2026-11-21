import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  effectiveValveOpeningIndex,
  getValveWheelKnobs,
  getValveTargets,
  nextWheelPhase,
  openingPercent,
  VALVE_INSTALLATION_CONTROL,
  VALVE_OPENING_CONTROLS,
  valueToOpeningIndex,
  valveIsAdjustable,
} from './throttlerValve'

function control(overrides: Partial<NumericControlSnapshot> = {}): NumericControlSnapshot {
  return {
    applied: 0,
    min: 0,
    max: 250_000,
    unit: 'tx/s',
    applyMode: 'immediate',
    ...overrides,
  }
}

describe('throttler valve mapping', () => {
  it('creates local integer targets spanning the complete range', () => {
    expect(getValveTargets(control())).toEqual([0, 50_000, 100_000, 150_000, 200_000, 250_000])
    expect(getValveTargets(control({ max: 4_000_000 }))).toEqual([
      0, 500_000, 1_000_000, 1_500_000, 2_000_000, 2_500_000, 3_000_000, 3_500_000, 4_000_000,
    ])
    expect(getValveTargets(control({ max: 250_001 }))).toEqual([
      0, 50_000, 100_000, 150_000, 200_000, 250_001,
    ])
  })

  it('maps values to the nearest opening and resolves ties downward', () => {
    const capability = control({ max: 110 })
    expect(valueToOpeningIndex(-1, capability)).toBe(0)
    expect(valueToOpeningIndex(15, capability)).toBe(1)
    expect(valueToOpeningIndex(200, capability)).toBe(11)
    expect(effectiveValveOpeningIndex(false, control())).toBe(11)
    expect(effectiveValveOpeningIndex(true, control({ applied: 25_000 }))).toBe(0)
    expect(effectiveValveOpeningIndex(null, control({ applied: 25_000 }))).toBe(0)
  })

  it('removes only the last interior target strictly closer than half a step to max', () => {
    expect(getValveTargets(control({ max: 104 }))?.slice(-2)).toEqual([90, 104])
    expect(getValveTargets(control({ max: 105 }))?.slice(-2)).toEqual([100, 105])
    expect(getValveTargets(control({ max: 106 }))?.slice(-2)).toEqual([100, 106])
    expect(getValveTargets(control({ min: 5, max: 6 }))).toEqual([5, 6])
    expect(
      getValveTargets(control({ min: Number.MAX_SAFE_INTEGER - 1, max: Number.MAX_SAFE_INTEGER })),
    ).toEqual([Number.MAX_SAFE_INTEGER - 1, Number.MAX_SAFE_INTEGER])
  })

  it.each([1, 10, 250_001, 4_000_000, Number.MAX_SAFE_INTEGER])(
    'includes both edges with safe unique integer targets for max=%s',
    (max) => {
      const capability = control({ max })
      const targets = getValveTargets(capability)!
      expect(targets[0]).toBe(0)
      expect(targets.at(-1)).toBe(max)
      expect(targets.length).toBeLessThanOrEqual(12)
      expect(new Set(targets).size).toBe(targets.length)
      expect(
        targets.every((value) => Number.isSafeInteger(value) && value >= 0 && value <= max),
      ).toBe(true)
      expect(effectiveValveOpeningIndex(true, control({ max, applied: max }))).toBe(11)
      expect(valveIsAdjustable(capability)).toBe(true)
    },
  )

  it('keeps visible side targets separate from the installed bypass grip', () => {
    const intersects = (
      left: { x: number; y: number; width: number; height: number },
      right: { x: number; y: number; width: number; height: number },
    ) =>
      left.x < right.x + right.width &&
      right.x < left.x + left.width &&
      left.y < right.y + right.height &&
      right.y < left.y + left.height

    expect(VALVE_OPENING_CONTROLS.decrease).toEqual({
      x: 394,
      y: 398,
      width: 26,
      height: 34,
    })
    expect(VALVE_OPENING_CONTROLS.increase).toEqual({
      x: 440,
      y: 398,
      width: 26,
      height: 34,
    })
    expect(VALVE_INSTALLATION_CONTROL.installedTarget).toEqual({
      x: 406,
      y: 368,
      width: 48,
      height: 29,
    })
    expect(intersects(VALVE_OPENING_CONTROLS.decrease, VALVE_OPENING_CONTROLS.increase)).toBe(false)
    expect(
      intersects(VALVE_OPENING_CONTROLS.decrease, VALVE_INSTALLATION_CONTROL.installedTarget),
    ).toBe(false)
    expect(
      intersects(VALVE_OPENING_CONTROLS.increase, VALVE_INSTALLATION_CONTROL.installedTarget),
    ).toBe(false)
    const contains = (
      target: { x: number; y: number; width: number; height: number },
      x: number,
      y: number,
    ) =>
      x >= target.x && x < target.x + target.width && y >= target.y && y < target.y + target.height

    expect(contains(VALVE_OPENING_CONTROLS.decrease, 404, 415)).toBe(true)
    expect(contains(VALVE_OPENING_CONTROLS.increase, 456, 415)).toBe(true)
  })

  it('disables adjustment for unavailable values and invalid ranges', () => {
    expect(valveIsAdjustable(control())).toBe(true)
    expect(valveIsAdjustable(control({ max: 0 }))).toBe(false)
    expect(valveIsAdjustable(control({ max: Number.MAX_SAFE_INTEGER + 1 }))).toBe(false)
    expect(valveIsAdjustable(control({ applied: null }))).toBe(false)
    expect(valveIsAdjustable(control({ applyMode: 'unavailable' }))).toBe(false)
  })

  it('keeps the accepted labels and cyclic wheel phases independent', () => {
    expect(Array.from({ length: 12 }, (_, index) => openingPercent(index))).toEqual([
      0, 9, 18, 27, 36, 45, 55, 64, 73, 82, 91, 100,
    ])
    expect(nextWheelPhase(5, 1)).toBe(0)
    expect(nextWheelPhase(0, -1)).toBe(5)
    expect(nextWheelPhase(1, 5)).toBe(0)
  })

  it('alternates fixed-ellipse knobs between exact 0 and 30 degree orbits', () => {
    const phases = Array.from({ length: 6 }, (_, phase) => getValveWheelKnobs(phase))

    expect(
      phases.map((knobs) =>
        [...knobs].map((knob) => knob.angle).sort((left, right) => left - right),
      ),
    ).toEqual([
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
    ])
    for (const knobs of phases) {
      expect(knobs.map((knob) => knob.depth)).toEqual(
        [...knobs].map((knob) => knob.depth).sort((a, b) => a - b),
      )
      expect(knobs.every((knob) => knob.layer === (knob.depth < 0 ? 'back' : 'front'))).toBe(true)
    }
  })
})
