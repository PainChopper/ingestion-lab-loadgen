import type {
  NumericControlSnapshot,
} from '../../model/loadgen'
import type { Point } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'

export type CapacityRange = Pick<
  NumericControlSnapshot,
  'min' | 'max' | 'step'
>

export type CapacityValues = readonly number[]

export interface CapacityTick {
  readonly value: number
  readonly y: number
  readonly major: boolean
}

export type ChannelCapacityRequestState = 'preview' | 'pending' | null

export interface ChannelCapacityPresentation {
  readonly applied: number
  readonly candidate: number
  readonly requestState: ChannelCapacityRequestState
}

export interface ChannelCableGeometryPresentation {
  readonly capacity: ChannelCapacityPresentation
  readonly cableY: number
  readonly sliderY: number
  readonly cableX: number
  readonly sliderX: number
  readonly cablePath: string
  readonly requestedPath: string | null
}

export const CHANNEL_CABLE_MAX_LIFT = 240
export const PORTRAIT_CHANNEL_CABLE_MAX_LIFT = 140

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
  values?: CapacityValues,
): number {
  if (!Number.isFinite(value)) return range.min

  if (values !== undefined && values.length > 0) {
    const bounded = Math.min(range.max, Math.max(range.min, value))
    return values.reduce((closest, candidate) =>
      Math.abs(candidate - bounded) < Math.abs(closest - bounded)
        ? candidate
        : closest,
    )
  }

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

function capacityIndex(
  value: number,
  range: CapacityRange,
  values: CapacityValues,
): number {
  return values.indexOf(normalizeCapacity(value, range, values))
}

export function getChannelCapacityPresentation(
  control: NumericControlSnapshot,
  localPreview: number | null = null,
  values?: CapacityValues,
): ChannelCapacityPresentation {
  const applied = normalizeCapacity(
    control.applied ?? control.min,
    control,
    values,
  )
  const candidateSource =
    localPreview ?? control.preview ?? control.pending ?? applied
  const candidate = normalizeCapacity(candidateSource, control, values)

  if (candidate === applied) {
    return { applied, candidate, requestState: null }
  }

  const pendingMatchesCandidate =
    control.pending !== null &&
    normalizeCapacity(control.pending, control, values) === candidate

  return {
    applied,
    candidate,
    requestState:
      localPreview === null && pendingMatchesCandidate ? 'pending' : 'preview',
  }
}

export function capacityToCableY(
  capacity: number,
  range: CapacityRange,
  baseline: number,
  maxLift: number,
  values?: CapacityValues,
): number {
  if (values !== undefined && values.length > 1 && maxLift > 0) {
    const index = capacityIndex(capacity, range, values)
    return baseline - (index / (values.length - 1)) * maxLift
  }

  const span = range.max - range.min
  if (span <= 0 || maxLift <= 0) return baseline

  const normalized = normalizeCapacity(capacity, range, values)
  const ratio = (normalized - range.min) / span
  return baseline - ratio * maxLift
}

export function cableYToCapacity(
  y: number,
  range: CapacityRange,
  baseline: number,
  maxLift: number,
  values?: CapacityValues,
): number {
  if (values !== undefined && values.length > 1 && maxLift > 0) {
    const ratio = Math.min(1, Math.max(0, (baseline - y) / maxLift))
    return values[Math.round(ratio * (values.length - 1))]!
  }

  if (maxLift <= 0 || range.max <= range.min) return range.min

  const ratio = Math.min(1, Math.max(0, (baseline - y) / maxLift))
  return normalizeCapacity(
    range.min + ratio * (range.max - range.min),
    range,
    values,
  )
}

export function capacityFromVerticalDrag(
  initialCapacity: number,
  pointerDeltaY: number,
  range: CapacityRange,
  maxLift: number,
  values?: CapacityValues,
): number {
  if (values !== undefined && values.length > 1 && maxLift > 0) {
    const initialIndex = capacityIndex(initialCapacity, range, values)
    const indexDelta = Math.round(
      (-pointerDeltaY / maxLift) * (values.length - 1),
    )
    const nextIndex = Math.min(
      values.length - 1,
      Math.max(0, initialIndex + indexDelta),
    )
    return values[nextIndex]!
  }

  if (maxLift <= 0 || range.max <= range.min) {
    return normalizeCapacity(initialCapacity, range, values)
  }

  const capacityDelta =
    (-pointerDeltaY / maxLift) * (range.max - range.min)
  return normalizeCapacity(initialCapacity + capacityDelta, range, values)
}

export function capacityFromKeyboard(
  key: string,
  currentCapacity: number,
  range: CapacityRange,
  values?: CapacityValues,
): number | null {
  if (values !== undefined && values.length > 0) {
    const currentIndex = capacityIndex(currentCapacity, range, values)
    let nextIndex: number
    switch (key) {
      case 'ArrowUp':
      case 'ArrowRight':
        nextIndex = currentIndex + 1
        break
      case 'ArrowDown':
      case 'ArrowLeft':
        nextIndex = currentIndex - 1
        break
      case 'PageUp':
        nextIndex = currentIndex + 5
        break
      case 'PageDown':
        nextIndex = currentIndex - 5
        break
      case 'Home':
        nextIndex = 0
        break
      case 'End':
        nextIndex = values.length - 1
        break
      default:
        return null
    }

    return values[Math.min(values.length - 1, Math.max(0, nextIndex))]!
  }

  let nextCapacity: number
  switch (key) {
    case 'ArrowUp':
    case 'ArrowRight':
      nextCapacity = currentCapacity + range.step
      break
    case 'ArrowDown':
    case 'ArrowLeft':
      nextCapacity = currentCapacity - range.step
      break
    case 'PageUp':
      nextCapacity = currentCapacity + range.step * 5
      break
    case 'PageDown':
      nextCapacity = currentCapacity - range.step * 5
      break
    case 'Home':
      nextCapacity = range.min
      break
    case 'End':
      nextCapacity = range.max
      break
    default:
      return null
  }

  return normalizeCapacity(nextCapacity, range, values)
}

export function buildChannelCablePath(
  start: Point,
  end: Point,
  topY: number,
): string {
  return `M${formatCoordinate(start.x)} ${formatCoordinate(start.y)} ${buildChannelCableCommands(start, end, topY)}`
}

export function buildPortraitChannelCablePath(
  start: Point,
  end: Point,
  leftX: number,
): string {
  const shift = Math.max(0, start.x - leftX)
  if (shift < 0.5) {
    return `M${formatCoordinate(start.x)} ${formatCoordinate(start.y)} V${formatCoordinate(end.y)}`
  }

  const height = end.y - start.y
  const shoulder = Math.min(46, height * 0.24)
  const topY = start.y + shoulder
  const bottomY = end.y - shoulder
  const radius = Math.min(16, shift / 2, (bottomY - topY) / 2)
  const f = formatCoordinate
  return [
    `M${f(start.x)} ${f(start.y)}`,
    `V${f(topY - radius)}`,
    `Q${f(start.x)} ${f(topY)} ${f(start.x - radius)} ${f(topY)}`,
    `H${f(leftX + radius)}`,
    `Q${f(leftX)} ${f(topY)} ${f(leftX)} ${f(topY + radius)}`,
    `V${f(bottomY - radius)}`,
    `Q${f(leftX)} ${f(bottomY)} ${f(leftX + radius)} ${f(bottomY)}`,
    `H${f(start.x - radius)}`,
    `Q${f(start.x)} ${f(bottomY)} ${f(start.x)} ${f(bottomY + radius)}`,
    `V${f(end.y)}`,
  ].join(' ')
}

function buildChannelCableCommands(
  start: Point,
  end: Point,
  topY: number,
): string {
  const baseline = start.y
  const lift = Math.max(0, baseline - topY)
  if (lift < 0.5) {
    return `H${formatCoordinate(end.x)}`
  }

  const width = end.x - start.x
  const shoulder = Math.min(46, width * 0.24)
  const leftX = start.x + shoulder
  const rightX = end.x - shoulder
  const radius = Math.min(16, lift / 2, (rightX - leftX) / 2)
  const f = formatCoordinate

  return [
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

export function getChannelCableGeometryPresentation(
  control: NumericControlSnapshot,
  start: Point,
  end: Point,
  localPreview: number | null = null,
  orientation: PipelineOrientation = 'landscape',
  maxLift = orientation === 'portrait'
    ? PORTRAIT_CHANNEL_CABLE_MAX_LIFT
    : CHANNEL_CABLE_MAX_LIFT,
  values?: CapacityValues,
): ChannelCableGeometryPresentation {
  const capacity = getChannelCapacityPresentation(control, localPreview, values)
  if (orientation === 'portrait') {
    const appliedX = capacityToCableY(
      capacity.applied,
      control,
      start.x,
      maxLift,
      values,
    )
    const candidateX = capacityToCableY(
      capacity.candidate,
      control,
      start.x,
      maxLift,
      values,
    )
    const cableX =
      localPreview === null && capacity.requestState === 'pending'
        ? candidateX
        : appliedX
    const cablePath = buildPortraitChannelCablePath(start, end, cableX)
    return {
      capacity,
      cableY: start.y,
      sliderY: (start.y + end.y) / 2,
      cableX,
      sliderX: candidateX,
      cablePath,
      requestedPath:
        localPreview === null
          ? null
          : buildPortraitChannelCablePath(start, end, candidateX),
    }
  }

  const appliedY = capacityToCableY(
    capacity.applied,
    control,
    start.y,
    CHANNEL_CABLE_MAX_LIFT,
    values,
  )
  const candidateY = capacityToCableY(
    capacity.candidate,
    control,
    start.y,
    CHANNEL_CABLE_MAX_LIFT,
    values,
  )
  const cableY =
    localPreview === null && capacity.requestState === 'pending'
      ? candidateY
      : appliedY
  const sliderY = candidateY
  const cablePath = buildChannelCablePath(start, end, cableY)

  return {
    capacity,
    cableY,
    sliderY,
    cableX: start.x,
    sliderX: (start.x + end.x) / 2,
    cablePath,
    requestedPath:
      localPreview === null
        ? null
        : buildChannelCablePath(start, end, sliderY),
  }
}

export function getCapacityTicks(
  range: CapacityRange,
  baseline: number,
  maxLift: number,
  values?: CapacityValues,
): readonly CapacityTick[] {
  if (values !== undefined && values.length > 0) {
    const middleIndex = Math.floor(values.length / 2)
    return values.map((value, index) => ({
      value,
      y: capacityToCableY(value, range, baseline, maxLift, values),
      major:
        index === 0 || index === middleIndex || index === values.length - 1,
    }))
  }

  const step = range.step > 0 ? range.step : 1
  const stepCount = Math.max(1, Math.round((range.max - range.min) / step))
  const tickStride = Math.max(1, Math.ceil(stepCount / 20))
  const middleIndex = Math.round(stepCount / 2)
  const tickValues = new Set<number>([range.min, range.max])

  for (let index = tickStride; index < stepCount; index += tickStride) {
    tickValues.add(normalizeCapacity(range.min + index * step, range))
  }
  tickValues.add(normalizeCapacity(range.min + middleIndex * step, range))

  return [...tickValues]
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
