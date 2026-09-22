import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  getValveWheelKnobs,
  getValveTargets,
  nextWheelPhase,
  openingPercent,
  VALVE_INSTALLATION_CONTROL,
  VALVE_OPENING_CONTROLS,
  valueToOpeningIndex,
  valveIsAdjustable,
} from './throttlerValve'

function control(
  overrides: Partial<NumericControlSnapshot> = {},
): NumericControlSnapshot {
  return {
    applied: 0,
    preview: null,
    pending: null,
    min: 0,
    max: 250_000,
    step: 5_000,
    unit: 'tx/s',
    applyMode: 'immediate',
    ...overrides,
  }
}

describe('throttler valve mapping', () => {
  it('creates 12 capability-anchored snapped absolute targets', () => {
    expect(getValveTargets(control())).toEqual([
      0, 25_000, 45_000, 70_000, 90_000, 115_000,
      135_000, 160_000, 180_000, 205_000, 225_000, 250_000,
    ])
    expect(getValveTargets(control({ min: 10, max: 120, step: 10 })))
      .toEqual([10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120])
  })

  it('maps applied values to the nearest opening and resolves ties downward', () => {
    const targets = getValveTargets(control())!
    expect(valueToOpeningIndex(-1, control(), targets)).toBe(0)
    expect(valueToOpeningIndex(35_000, control(), targets)).toBe(1)
    expect(valueToOpeningIndex(250_001, control(), targets)).toBe(11)
  })

  it('keeps the 12-stop current TPS policy mapping', () => {
    const currentPolicy = control({
      applied: 2_000_000,
      max: 4_000_000,
      step: 200_000,
    })
    const targets = getValveTargets(currentPolicy)!

    expect(targets).toEqual([
      0, 400_000, 800_000, 1_000_000, 1_400_000, 1_800_000,
      2_200_000, 2_600_000, 3_000_000, 3_200_000, 3_600_000, 4_000_000,
    ])
    expect(new Set(targets)).toHaveLength(12)
    expect(targets.every((target) =>
      target >= currentPolicy.min && target <= currentPolicy.max &&
      target % currentPolicy.step === 0
    )).toBe(true)
    expect(valueToOpeningIndex(2_000_000, currentPolicy, targets)).toBe(5)
  })

  it('keeps visible side targets separate from the installed bypass grip', () => {
    const intersects = (
      left: { x: number, y: number, width: number, height: number },
      right: { x: number, y: number, width: number, height: number },
    ) => left.x < right.x + right.width && right.x < left.x + left.width &&
      left.y < right.y + right.height && right.y < left.y + left.height

    expect(VALVE_OPENING_CONTROLS.decrease).toEqual({
      x: 394, y: 398, width: 26, height: 34,
    })
    expect(VALVE_OPENING_CONTROLS.increase).toEqual({
      x: 440, y: 398, width: 26, height: 34,
    })
    expect(VALVE_INSTALLATION_CONTROL.installedTarget).toEqual({
      x: 406, y: 368, width: 48, height: 29,
    })
    expect(intersects(
      VALVE_OPENING_CONTROLS.decrease,
      VALVE_OPENING_CONTROLS.increase,
    )).toBe(false)
    expect(intersects(
      VALVE_OPENING_CONTROLS.decrease,
      VALVE_INSTALLATION_CONTROL.installedTarget,
    )).toBe(false)
    expect(intersects(
      VALVE_OPENING_CONTROLS.increase,
      VALVE_INSTALLATION_CONTROL.installedTarget,
    )).toBe(false)
    const contains = (
      target: { x: number, y: number, width: number, height: number },
      x: number,
      y: number,
    ) => x >= target.x && x < target.x + target.width &&
      y >= target.y && y < target.y + target.height

    expect(contains(VALVE_OPENING_CONTROLS.decrease, 404, 415)).toBe(true)
    expect(contains(VALVE_OPENING_CONTROLS.increase, 456, 415)).toBe(true)
  })

  it('disables adjustment unless all 12 snapped targets are distinct', () => {
    expect(valveIsAdjustable(control())).toBe(true)
    expect(valveIsAdjustable(control({ max: 10, step: 2 }))).toBe(false)
    expect(valveIsAdjustable(control({ step: 0 }))).toBe(false)
    expect(valveIsAdjustable(control({ applied: null }))).toBe(false)
    expect(valveIsAdjustable(control({ applyMode: 'unavailable' }))).toBe(false)
  })

  it('keeps the accepted labels and cyclic wheel phases independent', () => {
    expect(Array.from({ length: 12 }, (_, index) => openingPercent(index)))
      .toEqual([0, 9, 18, 27, 36, 45, 55, 64, 73, 82, 91, 100])
    expect(nextWheelPhase(5, 1)).toBe(0)
    expect(nextWheelPhase(0, -1)).toBe(5)
    expect(nextWheelPhase(1, 5)).toBe(0)
  })

  it('alternates fixed-ellipse knobs between exact 0 and 30 degree orbits', () => {
    const phases = Array.from({ length: 6 }, (_, phase) =>
      getValveWheelKnobs(phase)
    )

    expect(phases.map((knobs) =>
      [...knobs].map((knob) => knob.angle).sort((left, right) => left - right)
    )).toEqual([
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
    ])
    for (const knobs of phases) {
      expect(knobs.map((knob) => knob.depth))
        .toEqual([...knobs].map((knob) => knob.depth).sort((a, b) => a - b))
      expect(knobs.every((knob) =>
        knob.layer === (knob.depth < 0 ? 'back' : 'front')
      )).toBe(true)
    }
  })
})
