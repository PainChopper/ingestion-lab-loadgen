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
  readonly legCount: ChannelCableLegCount
  readonly cableY: number
  readonly sliderY: number
  readonly cableX: number
  readonly sliderX: number
  readonly cablePath: string
  readonly requestedPath: string | null
}

export interface Box {
  readonly x: number
  readonly y: number
  readonly width: number
  readonly height: number
}

export type ChannelCableLegCount = 0 | 2 | 4 | 6 | 8 | 10 | 12 | 14

export interface ChannelCapacityGeometryPresentation {
  readonly cablePath: string
  readonly legCount: ChannelCableLegCount
  readonly desiredLegCount: ChannelCableLegCount
  readonly scaleForbiddenBox: Box
}

export const CHANNEL_CABLE_MAX_LIFT = 240
export const PORTRAIT_CHANNEL_CABLE_MAX_LIFT = 140
const CHANNEL_HANDLE_HALF_HEIGHT = 12
const CHANNEL_CABLE_MAX_STROKE_WIDTH = 10
const CHANNEL_SCALE_HOSE_CLEARANCE = 8
export const CHANNEL_CAPACITY_SCALE_OFFSET = CHANNEL_HANDLE_HALF_HEIGHT +
  CHANNEL_CABLE_MAX_STROKE_WIDTH / 2 + CHANNEL_SCALE_HOSE_CLEARANCE

const SERPENTINE_MIN_LEG_GAP = 4
const SERPENTINE_CORNER_RADIUS = 8

export function getCapacityLegCount(
  control: NumericControlSnapshot,
  values?: CapacityValues,
): ChannelCableLegCount {
  const applied = control.applied
  if (applied === null || applied <= 0 || control.max <= 0) return 0

  const positiveValues = values?.filter((value) =>
    Number.isFinite(value) && value > 0 && value <= control.max)
  const minPositive = positiveValues !== undefined && positiveValues.length > 0
    ? Math.min(...positiveValues)
    : control.min > 0
      ? control.min
      : control.step > 0
        ? control.min + control.step
        : control.max
  const maxPositive = positiveValues !== undefined && positiveValues.length > 0
    ? Math.max(...positiveValues)
    : control.max
  if (minPositive <= 0 || maxPositive <= 0) return 0
  if (maxPositive <= minPositive) return 2

  const normalized = Math.min(maxPositive, Math.max(minPositive, applied))
  const scale = Math.log(normalized / minPositive) /
    Math.log(maxPositive / minPositive)
  const bucket = Math.floor(6 * Math.min(1, Math.max(0, scale)))
  return (2 * (1 + bucket)) as ChannelCableLegCount
}

function fittedLegCount(
  desired: ChannelCableLegCount,
  availableSpan: number,
): ChannelCableLegCount {
  for (let candidate = desired; candidate >= 2; candidate -= 2) {
    if (availableSpan >= (candidate - 1) * SERPENTINE_MIN_LEG_GAP) {
      return candidate as ChannelCableLegCount
    }
  }
  return 0
}

function spacedCoordinates(
  start: number,
  end: number,
  count: number,
): readonly number[] {
  if (count === 1) return [(start + end) / 2]
  return Array.from(
    { length: count },
    (_, index) => start + (index / (count - 1)) * (end - start),
  )
}

function spacedCoordinatesAroundForbiddenRange(
  start: number,
  end: number,
  count: number,
  forbiddenStart: number,
  forbiddenEnd: number,
): readonly number[] {
  const before = Math.max(0, forbiddenStart - start)
  const after = Math.max(0, end - forbiddenEnd)
  const available = before + after
  if (available <= 0) return []

  return spacedCoordinates(0, available, count).map((offset) =>
    offset <= before ? start + offset : forbiddenEnd + offset - before)
}

interface AxisRange {
  readonly start: number
  readonly end: number
}

function mergeAxisRanges(
  start: number,
  end: number,
  ranges: readonly AxisRange[],
): readonly AxisRange[] {
  const sorted = ranges
    .map((range) => ({
      start: Math.max(start, range.start),
      end: Math.min(end, range.end),
    }))
    .filter((range) => range.end > range.start)
    .sort((left, right) => left.start - right.start)
  const merged: AxisRange[] = []
  for (const range of sorted) {
    const previous = merged[merged.length - 1]
    if (previous === undefined || range.start > previous.end) {
      merged.push(range)
    } else {
      merged[merged.length - 1] = {
        start: previous.start,
        end: Math.max(previous.end, range.end),
      }
    }
  }
  return merged
}

function spacedCoordinatesAroundForbiddenRanges(
  start: number,
  end: number,
  count: number,
  forbidden: readonly AxisRange[],
): readonly number[] {
  const ranges = mergeAxisRanges(start, end, forbidden)
  const available = end - start - ranges.reduce(
    (total, range) => total + range.end - range.start,
    0,
  )
  if (available <= 0) return []

  return spacedCoordinates(0, available, count).map((offset) => {
    let coordinate = start + offset
    for (const range of ranges) {
      if (coordinate < range.start) break
      coordinate += range.end - range.start
    }
    return coordinate
  })
}

function buildRoundedOrthogonalPath(
  points: readonly Point[],
  radius: number,
  legSegments: ReadonlySet<number> = new Set(),
): string {
  const f = formatCoordinate
  const compact = points.filter((point, index) => index === 0 ||
    point.x !== points[index - 1]!.x || point.y !== points[index - 1]!.y)
  if (compact.length === 2) {
    return `M${f(compact[0]!.x)} ${f(compact[0]!.y)} L${f(compact[1]!.x)} ${f(compact[1]!.y)}`
  }

  const cornerRadius = compact.slice(1, -1).reduce((smallest, corner, index) => {
    const previous = compact[index]!
    const next = compact[index + 2]!
    return Math.min(
      smallest,
      (Math.abs(corner.x - previous.x) + Math.abs(corner.y - previous.y)) / 2,
      (Math.abs(next.x - corner.x) + Math.abs(next.y - corner.y)) / 2,
    )
  }, radius)
  const commands = [`M${f(compact[0]!.x)} ${f(compact[0]!.y)}`]
  const appendStraight = (from: Point, to: Point, segment: number) => {
    if (legSegments.has(segment)) {
      if (from.x === to.x) return `V${f(to.y)}`
      if (from.y === to.y) return `H${f(to.x)}`
    }
    return `L${f(to.x)} ${f(to.y)}`
  }
  for (let index = 1; index < compact.length - 1; index += 1) {
    const previous = compact[index - 1]!
    const corner = compact[index]!
    const next = compact[index + 1]!
    const before = {
      x: corner.x + Math.sign(previous.x - corner.x) * cornerRadius,
      y: corner.y + Math.sign(previous.y - corner.y) * cornerRadius,
    }
    const after = {
      x: corner.x + Math.sign(next.x - corner.x) * cornerRadius,
      y: corner.y + Math.sign(next.y - corner.y) * cornerRadius,
    }
    commands.push(
      appendStraight(previous, before, index - 1),
      `Q${f(corner.x)} ${f(corner.y)} ${f(after.x)} ${f(after.y)}`,
    )
  }
  const end = compact[compact.length - 1]!
  const previous = compact[compact.length - 2]!
  if (previous.x === end.x) commands.push(`V${f(end.y)}`)
  else if (previous.y === end.y) commands.push(`H${f(end.x)}`)
  else commands.push(`L${f(end.x)} ${f(end.y)}`)
  return commands.join(' ')
}

function buildLandscapeCapacityPath(
  start: Point,
  end: Point,
  legCount: ChannelCableLegCount,
  forbidden: Box,
): string {
  const topY = start.y - 190
  if (legCount === 0) {
    return buildRoundedOrthogonalPath([start, end], SERPENTINE_CORNER_RADIUS)
  }

  const legs = spacedCoordinatesAroundForbiddenRange(
    start.x + 24,
    end.x - 24,
    legCount,
    forbidden.x,
    forbidden.x + forbidden.width,
  )
  const points: Point[] = [start]
  let y = start.y
  for (const x of legs) {
    const previous = points[points.length - 1]!
    const crossesMarker = previous.x < forbidden.x &&
      x > forbidden.x + forbidden.width &&
      y > forbidden.y && y < forbidden.y + forbidden.height
    if (crossesMarker) {
      const above = y - forbidden.y
      const below = forbidden.y + forbidden.height - y
      const neckY = above < below
        ? forbidden.y
        : forbidden.y + forbidden.height
      points.push(
        { x: forbidden.x, y },
        { x: forbidden.x, y: neckY },
        { x: forbidden.x + forbidden.width, y: neckY },
        { x: forbidden.x + forbidden.width, y },
      )
    }
    points.push({ x, y })
    y = y === start.y ? topY : start.y
    points.push({ x, y })
  }
  points.push(end)
  const legSegments = new Set<number>()
  for (let index = 0; index < points.length - 1; index += 1) {
    if (points[index]!.x === points[index + 1]!.x) {
      legSegments.add(index)
    }
  }
  return buildRoundedOrthogonalPath(points, SERPENTINE_CORNER_RADIUS, legSegments)
}

function buildPortraitCapacityPath(
  start: Point,
  end: Point,
  legCount: ChannelCableLegCount,
  forbidden: readonly Box[],
): string {
  const outerX = Math.max(
    start.x + 60,
    ...forbidden.map((box) =>
      box.x + box.width + CHANNEL_SCALE_HOSE_CLEARANCE),
  )
  if (legCount === 0) {
    if (forbidden.length === 1) {
      return buildRoundedOrthogonalPath([start, end], SERPENTINE_CORNER_RADIUS)
    }
    return buildRoundedOrthogonalPath(
      [start, { x: outerX, y: start.y }, { x: outerX, y: end.y }, end],
      SERPENTINE_CORNER_RADIUS,
    )
  }

  const legs = spacedCoordinatesAroundForbiddenRanges(
    start.y + 28,
    end.y - 28,
    legCount,
    forbidden.map((box) => ({
      start: box.y - CHANNEL_SCALE_HOSE_CLEARANCE,
      end: box.y + box.height + CHANNEL_SCALE_HOSE_CLEARANCE,
    })),
  )
  const points: Point[] = [start]
  let x = start.x
  for (const y of legs) {
    points.push({ x, y })
    x = x === start.x ? outerX : start.x
    points.push({ x, y })
  }
  points.push(end)
  const legSegments = new Set<number>()
  for (let index = 0; index < points.length - 1; index += 1) {
    if (points[index]!.y === points[index + 1]!.y) {
      legSegments.add(index)
    }
  }
  return buildRoundedOrthogonalPath(points, SERPENTINE_CORNER_RADIUS, legSegments)
}

export function getChannelCapacityGeometryPresentation(
  control: NumericControlSnapshot,
  start: Point,
  end: Point,
  orientation: PipelineOrientation = 'landscape',
  values?: CapacityValues,
  hoseForbiddenBoxes: readonly Box[] = [],
): ChannelCapacityGeometryPresentation {
  const desiredLegCount = getCapacityLegCount(control, values)
  if (orientation === 'portrait') {
    const centerY = (start.y + end.y) / 2 - CHANNEL_CAPACITY_SCALE_OFFSET
    const sliderX = capacityToCableY(
      control.applied ?? control.min,
      control,
      start.x,
      PORTRAIT_CHANNEL_CABLE_MAX_LIFT,
      values,
    )
    const scaleForbiddenBox = {
      x: sliderX - 20,
      y: centerY - 26,
      width: 40,
      height: 52,
    }
    const forbiddenBoxes = [scaleForbiddenBox, ...hoseForbiddenBoxes]
    const forbiddenRanges = mergeAxisRanges(
      start.y + 28,
      end.y - 28,
      forbiddenBoxes.map((box) => ({
        start: box.y - CHANNEL_SCALE_HOSE_CLEARANCE,
        end: box.y + box.height + CHANNEL_SCALE_HOSE_CLEARANCE,
      })),
    )
    const availableSpan = end.y - start.y - 56 - forbiddenRanges.reduce(
      (total, range) => total + range.end - range.start,
      0,
    )
    const legCount = fittedLegCount(desiredLegCount, availableSpan)
    return {
      cablePath: buildPortraitCapacityPath(
        start,
        end,
        legCount,
        forbiddenBoxes,
      ),
      legCount,
      desiredLegCount,
      scaleForbiddenBox,
    }
  }

  const centerX = (start.x + end.x) / 2
  const sliderY = capacityToCableY(
    control.applied ?? control.min,
    control,
    start.y - CHANNEL_CAPACITY_SCALE_OFFSET,
    CHANNEL_CABLE_MAX_LIFT,
    values,
  )
  const scaleForbiddenBox = {
    x: centerX - 26,
    y: sliderY - 20,
    width: 52,
    height: 40,
  }
  const availableSpan = end.x - start.x - 48 - scaleForbiddenBox.width
  const legCount = fittedLegCount(desiredLegCount, availableSpan)
  return {
    cablePath: buildLandscapeCapacityPath(
      start,
      end,
      legCount,
      scaleForbiddenBox,
    ),
    legCount,
    desiredLegCount,
    scaleForbiddenBox,
  }
}

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
  hoseForbiddenBoxes?: readonly Box[],
): ChannelCableGeometryPresentation {
  const capacity = getChannelCapacityPresentation(control, localPreview, values)
  const appliedGeometry = getChannelCapacityGeometryPresentation(
    { ...control, applied: capacity.applied },
    start,
    end,
    orientation,
    values,
    hoseForbiddenBoxes,
  )
  const requestedGeometry = localPreview === null
    ? null
    : getChannelCapacityGeometryPresentation(
        { ...control, applied: capacity.candidate },
        start,
        end,
        orientation,
        values,
        hoseForbiddenBoxes,
      )
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
    return {
      capacity,
      legCount: appliedGeometry.legCount,
      cableY: start.y,
      sliderY: (start.y + end.y) / 2 - CHANNEL_CAPACITY_SCALE_OFFSET,
      cableX: appliedX,
      sliderX: candidateX,
      cablePath: appliedGeometry.cablePath,
      requestedPath: requestedGeometry?.cablePath ?? null,
    }
  }

  const appliedY = capacityToCableY(
    capacity.applied,
    control,
    start.y - CHANNEL_CAPACITY_SCALE_OFFSET,
    CHANNEL_CABLE_MAX_LIFT,
    values,
  )
  const candidateY = capacityToCableY(
    capacity.candidate,
    control,
    start.y - CHANNEL_CAPACITY_SCALE_OFFSET,
    CHANNEL_CABLE_MAX_LIFT,
    values,
  )
  const sliderY = candidateY

  return {
    capacity,
    legCount: appliedGeometry.legCount,
    cableY: appliedY,
    sliderY,
    cableX: start.x,
    sliderX: (start.x + end.x) / 2,
    cablePath: appliedGeometry.cablePath,
    requestedPath: requestedGeometry?.cablePath ?? null,
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
