import { describe, expect, it } from 'vitest'
import { formatInteger, formatMilliseconds, formatRate } from './formatters'

describe('pipeline formatters', () => {
  it('formats numeric snapshot values with stable units', () => {
    expect(formatInteger(120000)).toBe('120,000')
    expect(formatRate(120000)).toBe('120,000 tx/s')
    expect(formatMilliseconds(1250)).toBe('1,250 ms')
  })

  it('uses an em dash for unavailable values while preserving zero', () => {
    expect(formatInteger(null)).toBe('—')
    expect(formatRate(null)).toBe('—')
    expect(formatMilliseconds(null)).toBe('—')
    expect(formatRate(0)).toBe('0 tx/s')
  })
})
