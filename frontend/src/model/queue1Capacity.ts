export const QUEUE1_CAPACITY_VALUES = Object.freeze([
  0,
  1,
  2,
  4,
  8,
  16,
  32,
  64,
  128,
  256,
  512,
  1_024,
  2_048,
  4_096,
  8_192,
] as const)

export function isQueue1Capacity(value: unknown): value is number {
  return typeof value === 'number' &&
    QUEUE1_CAPACITY_VALUES.includes(value as never)
}
