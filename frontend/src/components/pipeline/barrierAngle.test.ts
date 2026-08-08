import { describe, expect, it } from 'vitest'
import { getBarrierAngle } from './barrierAngle'

describe('getBarrierAngle', () => {
  it('maps admitted throughput to a stable barrier angle', () => {
    expect(getBarrierAngle(100, 100, 'running')).toBe(-90)
    expect(getBarrierAngle(100, 50, 'running')).toBe(-45)
    expect(getBarrierAngle(100, 0, 'running')).toBe(0)
    expect(getBarrierAngle(100, 100, 'paused')).toBe(0)
  })
})
