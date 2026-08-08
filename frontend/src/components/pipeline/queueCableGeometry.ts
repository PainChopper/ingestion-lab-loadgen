import type {
  NumericControlSnapshot,
  QueueFlowState,
} from '../../model/loadgen'
import type { Point } from './geometry'

export type CapacityRange = Pick<
  NumericControlSnapshot,
  'min' | 'max' | 'step'
>

export interface CapacityTick {
  readonly value: number
  readonly y: number
  readonly major: boolean
}

export const QUEUE_CABLE_MAX_MARKERS = 12

function decimalPlaces(value: number): number {
  const [, fraction = ''] = String(value).split('.')
  return fraction.length
}

function formatCoordinate(value: number): string {
  return Number(value.toFixed(2)).toString()
}

export function normalizeCapacity(
  value: number,
  range: CapacityRange,
): number {
  if (!Number.isFinite(value)) return range.min

  const step = range.step > 0 ? range.step : 1
  const bounded = Math.min(range.max, Math.max(range.min, value))
  const stepped = range.min + Math.round((bounded - range.min) / step) * step
  const precision = Math.max(
    decimalPlaces(range.min),
    decimalPlaces(range.max),
    decimalPlaces(step),
  )

  return Number(
    Math.min(range.max, Math.max(range.min, stepped)).toFixed(precision),
  )
}

export function capacityToCableY(
  capacity: number,
  range: CapacityRange,
  baseline: number,
  maxLift: number,
): number {
  const span = range.max - range.min
  if (span <= 0 || maxLift <= 0) return baseline

  const normalized = normalizeCapacity(capacity, range)
  const ratio = (normalized - range.min) / span
  return baseline - ratio * maxLift
}

export function cableYToCapacity(
  y: number,
  range: CapacityRange,
  baseline: number,
  maxLift: number,
): number {
  if (maxLift <= 0 || range.max <= range.min) return range.min

  const ratio = Math.min(1, Math.max(0, (baseline - y) / maxLift))
  return normalizeCapacity(range.min + ratio * (range.max - range.min), range)
}

export function capacityFromVerticalDrag(
  initialCapacity: number,
  pointerDeltaY: number,
  range: CapacityRange,
  maxLift: number,
): number {
  if (maxLift <= 0 || range.max <= range.min) {
    return normalizeCapacity(initialCapacity, range)
  }

  const capacityDelta =
    (-pointerDeltaY / maxLift) * (range.max - range.min)
  return normalizeCapacity(initialCapacity + capacityDelta, range)
}

export function buildQueueCablePath(
  start: Point,
  end: Point,
  topY: number,
): string {
  const baseline = start.y
  const lift = Math.max(0, baseline - topY)
  if (lift < 0.5) {
    return `M${formatCoordinate(start.x)} ${formatCoordinate(baseline)} H${formatCoordinate(end.x)}`
  }

  const width = end.x - start.x
  const shoulder = Math.min(46, width * 0.24)
  const leftX = start.x + shoulder
  const rightX = end.x - shoulder
  const radius = Math.min(16, lift / 2, (rightX - leftX) / 2)
  const f = formatCoordinate

  return [
    `M${f(start.x)} ${f(baseline)}`,
    `H${f(leftX - radius)}`,
    `Q${f(leftX)} ${f(baseline)} ${f(leftX)} ${f(baseline - radius)}`,
    `V${f(topY + radius)}`,
    `Q${f(leftX)} ${f(topY)} ${f(leftX + radius)} ${f(topY)}`,
    `H${f(rightX - radius)}`,
    `Q${f(rightX)} ${f(topY)} ${f(rightX)} ${f(topY + radius)}`,
    `V${f(baseline - radius)}`,
    `Q${f(rightX)} ${f(baseline)} ${f(rightX + radius)} ${f(baseline)}`,
    `H${f(end.x)}`,
  ].join(' ')
}

export function getCapacityTicks(
  range: CapacityRange,
  baseline: number,
  maxLift: number,
): readonly CapacityTick[] {
  const step = range.step > 0 ? range.step : 1
  const stepCount = Math.max(1, Math.round((range.max - range.min) / step))
  const tickStride = Math.max(1, Math.ceil(stepCount / 20))
  const middleIndex = Math.round(stepCount / 2)
  const values = new Set<number>([range.min, range.max])

  for (let index = tickStride; index < stepCount; index += tickStride) {
    values.add(normalizeCapacity(range.min + index * step, range))
  }
  values.add(normalizeCapacity(range.min + middleIndex * step, range))

  return [...values]
    .sort((left, right) => left - right)
    .map((value) => ({
      value,
      y: capacityToCableY(value, range, baseline, maxLift),
      major:
        value === range.min ||
        value === range.max ||
        value === normalizeCapacity(range.min + middleIndex * step, range),
    }))
}

export function getQueueMarkerCount(
  depthBatches: number | null,
  capacity: number,
  throughputTps: number | null,
  flowState: QueueFlowState,
): number {
  if (flowState === 'connection-error') return 0

  if (depthBatches !== null && depthBatches > 0) {
    const fillRatio = capacity > 0 ? Math.min(1, depthBatches / capacity) : 1
    return Math.max(1, Math.ceil(fillRatio * QUEUE_CABLE_MAX_MARKERS))
  }

  return flowState !== 'stopped' && throughputTps !== null && throughputTps > 0
    ? 1
    : 0
}
