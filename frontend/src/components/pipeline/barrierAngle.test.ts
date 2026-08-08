import { describe, expect, it } from 'vitest'
import { getBarrierAngle } from './barrierAngle'

describe('getBarrierAngle', () => {
  const control = (applied: number | null) => ({
    applied,
    min: 0,
    max: 200_000,
  })

  it('maps the applied requested setpoint across its control range', () => {
    expect(getBarrierAngle(control(0))).toBe(0)
    expect(getBarrierAngle(control(50_000))).toBe(-22.5)
    expect(getBarrierAngle(control(100_000))).toBe(-45)
    expect(getBarrierAngle(control(200_000))).toBe(-90)
  })

  it('clamps invalid setpoints without depending on runtime telemetry', () => {
    expect(getBarrierAngle(control(null))).toBe(0)
    expect(getBarrierAngle(control(-10_000))).toBe(0)
    expect(getBarrierAngle(control(250_000))).toBe(-90)
    expect(getBarrierAngle({ applied: 100, min: 10, max: 10 })).toBe(0)
  })
})
