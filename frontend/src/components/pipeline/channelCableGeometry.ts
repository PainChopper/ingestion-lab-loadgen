import type { ChannelCapacitySnapshot } from '../../model/loadgen'
import type { Point } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'

export type CapacityValues = readonly number[]

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
}

export const LANDSCAPE_CHANNEL_CABLE_HEIGHT = 190
const PORTRAIT_LABEL_CLEARANCE = 8

const SERPENTINE_MIN_LEG_GAP = 4
const SERPENTINE_CORNER_RADIUS = 8

export function getCapacityLegCount(
  control: ChannelCapacitySnapshot,
  values?: CapacityValues,
): ChannelCableLegCount {
  const applied = control.applied
  if (applied === null || applied <= 0 || control.max <= 0) return 0

  const positiveValues = values?.filter(
    (value) => Number.isFinite(value) && value > 0 && value <= control.max,
  )
  const minPositive =
    positiveValues !== undefined && positiveValues.length > 0
      ? Math.min(...positiveValues)
      : control.min > 0
        ? control.min
        : control.step > 0
          ? control.min + control.step
          : control.max
  const maxPositive =
    positiveValues !== undefined && positiveValues.length > 0
      ? Math.max(...positiveValues)
      : control.max
  if (minPositive <= 0 || maxPositive <= 0) return 0
  if (maxPositive <= minPositive) return 2

  const normalized = Math.min(maxPositive, Math.max(minPositive, applied))
  const scale = Math.log(normalized / minPositive) / Math.log(maxPositive / minPositive)
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

function spacedCoordinates(start: number, end: number, count: number): readonly number[] {
  if (count === 1) return [(start + end) / 2]
  return Array.from({ length: count }, (_, index) => start + (index / (count - 1)) * (end - start))
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
  const available =
    end - start - ranges.reduce((total, range) => total + range.end - range.start, 0)
  if (available <= 0) return []

  return spacedCoordinates(0, available, count).map((offset) => {
    let coordinate = start + offset
    for (const range of ranges) {
      if (coordinate <= range.start) break
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
  const compact = points.filter(
    (point, index) =>
      index === 0 || point.x !== points[index - 1]!.x || point.y !== points[index - 1]!.y,
  )
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
): string {
  const topY = start.y - LANDSCAPE_CHANNEL_CABLE_HEIGHT
  if (legCount === 0) {
    return buildRoundedOrthogonalPath([start, end], SERPENTINE_CORNER_RADIUS)
  }

  const legs = spacedCoordinates(start.x + 24, end.x - 24, legCount)
  const points: Point[] = [start]
  let y = start.y
  for (const x of legs) {
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
    ...forbidden.map((box) => box.x + box.width + PORTRAIT_LABEL_CLEARANCE),
  )
  if (legCount === 0) {
    if (forbidden.length === 0) {
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
      start: box.y - PORTRAIT_LABEL_CLEARANCE,
      end: box.y + box.height + PORTRAIT_LABEL_CLEARANCE,
    })),
  )
  const points: Point[] = [start]
  const appendVertical = (x: number, y: number) => {
    const previousY = points[points.length - 1]!.y
    const crossings = mergeAxisRanges(
      previousY,
      y,
      forbidden
        .filter(
          (box) =>
            x > box.x - PORTRAIT_LABEL_CLEARANCE &&
            x < box.x + box.width + PORTRAIT_LABEL_CLEARANCE,
        )
        .map((box) => ({
          start: box.y - PORTRAIT_LABEL_CLEARANCE,
          end: box.y + box.height + PORTRAIT_LABEL_CLEARANCE,
        })),
    )
    for (const crossing of crossings) {
      points.push(
        { x, y: crossing.start },
        { x: outerX, y: crossing.start },
        { x: outerX, y: crossing.end },
        { x, y: crossing.end },
      )
    }
    points.push({ x, y })
  }
  let x = start.x
  for (const [index, legY] of legs.entries()) {
    const y = forbidden.length > 0 && index === legs.length - 1 ? end.y : legY
    appendVertical(x, y)
    x = x === start.x ? outerX : start.x
    points.push({ x, y })
  }
  appendVertical(x, end.y)
  const legSegments = new Set<number>()
  for (let index = 0; index < points.length - 1; index += 1) {
    if (points[index]!.y === points[index + 1]!.y) {
      legSegments.add(index)
    }
  }
  return buildRoundedOrthogonalPath(points, SERPENTINE_CORNER_RADIUS, legSegments)
}

export function getChannelCapacityGeometryPresentation(
  control: ChannelCapacitySnapshot,
  start: Point,
  end: Point,
  orientation: PipelineOrientation = 'landscape',
  values?: CapacityValues,
  hoseForbiddenBoxes: readonly Box[] = [],
): ChannelCapacityGeometryPresentation {
  const desiredLegCount = getCapacityLegCount(control, values)
  if (orientation === 'portrait') {
    const forbiddenRanges = mergeAxisRanges(
      start.y + 28,
      end.y - 28,
      hoseForbiddenBoxes.map((box) => ({
        start: box.y - PORTRAIT_LABEL_CLEARANCE,
        end: box.y + box.height + PORTRAIT_LABEL_CLEARANCE,
      })),
    )
    const availableSpan =
      end.y -
      start.y -
      56 -
      forbiddenRanges.reduce((total, range) => total + range.end - range.start, 0)
    const legCount = fittedLegCount(desiredLegCount, availableSpan)
    return {
      cablePath: buildPortraitCapacityPath(start, end, legCount, hoseForbiddenBoxes),
      legCount,
      desiredLegCount,
    }
  }

  const availableSpan = end.x - start.x - 48
  const legCount = fittedLegCount(desiredLegCount, availableSpan)
  return {
    cablePath: buildLandscapeCapacityPath(start, end, legCount),
    legCount,
    desiredLegCount,
  }
}

function formatCoordinate(value: number): string {
  return Number(value.toFixed(2)).toString()
}
