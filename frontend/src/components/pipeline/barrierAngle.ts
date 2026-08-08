import type { NumericControlSnapshot } from '../../model/loadgen'

export function getBarrierAngle(
  requestedTps: Pick<NumericControlSnapshot, 'applied' | 'min' | 'max'>,
): number {
  const span = requestedTps.max - requestedTps.min
  if (requestedTps.applied === null || span <= 0) return 0

  const ratio = Math.min(
    1,
    Math.max(0, (requestedTps.applied - requestedTps.min) / span),
  )
  if (ratio === 0) return 0

  return -90 * ratio
}
