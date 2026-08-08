import type { RunState } from '../../model/loadgen'

export function getBarrierAngle(
  requestedTps: number | null,
  admittedTps: number | null,
  runState: RunState,
): number {
  if (
    runState !== 'running' ||
    requestedTps === null ||
    admittedTps === null ||
    requestedTps <= 0 ||
    admittedTps <= 0
  ) {
    return 0
  }

  const admittedRatio = Math.min(1, Math.max(0, admittedTps / requestedTps))
  return -90 * admittedRatio
}
