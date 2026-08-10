import { describe, expect, it } from 'vitest'
import type { NumericControlSnapshot } from '../../model/loadgen'
import {
  getValveWheelKnobs,
  getValveTargets,
  nextWheelPhase,
  openingPercent,
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
