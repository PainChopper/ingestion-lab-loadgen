import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import type { Point } from './geometry'
import { ACTOR_GEOMETRY } from './geometry'
import {
  buildQueueCablePath,
  buildQueueTraversalPath,
  capacityToCableY,
  getQueueCapacityPresentation,
  QUEUE_CABLE_MAX_LIFT,
} from './queueCableGeometry'

interface QueueTraversalGeometry {
  readonly upstream: readonly Point[]
  readonly start: Point
  readonly end: Point
  readonly downstream: readonly Point[]
}

export interface QueueMarkerPathGeometry {
  readonly occupancyPath: string
  readonly transientPath: string
  readonly occupancyLength: number
  readonly flowLength: number
  readonly handoffLength: number
  readonly flowEndRatio: number
  readonly handoffStartRatio: number
}

const TRANSIENT_OFFSET_Y = 12
const HANDLE_CLEARANCE = 30

const QUEUE_TRAVERSALS: Readonly<Record<QueueId, QueueTraversalGeometry>> = {
  'reader-to-throttler': {
    upstream: [
      { x: 96, y: 392 },
      { x: 122, y: 400 },
    ],
    start: ACTOR_GEOMETRY.reader.ports.output,
    end: ACTOR_GEOMETRY.throttler.ports.input,
    downstream: [
      { x: 377, y: 403 },
      { x: 384, y: 394 },
    ],
  },
  'throttler-to-sender': {
    upstream: [
      { x: 432, y: 394 },
      { x: 470, y: 403 },
    ],
    start: ACTOR_GEOMETRY.throttler.ports.output,
    end: ACTOR_GEOMETRY.sender.ports.input,
    downstream: [
      { x: 750, y: 403 },
      { x: 768, y: 394 },
      { x: 790, y: 394 },
    ],
  },
}

export const HTTP_TRAVERSAL_POINTS: readonly Point[] = Object.freeze([
  { x: 790, y: 397 },
  { x: 812, y: 415 },
  ACTOR_GEOMETRY.sender.ports.output,
  ACTOR_GEOMETRY.target.ports.input,
  { x: 962, y: 415 },
])

function pointDistance(left: Point, right: Point): number {
  return Math.hypot(right.x - left.x, right.y - left.y)
}

function polylineLength(points: readonly Point[]): number {
  let length = 0
  for (let index = 1; index < points.length; index += 1) {
    length += pointDistance(points[index - 1], points[index])
  }
  return length
}

function appliedTopY(control: NumericControlSnapshot, baseline: number): number {
  const capacity = getQueueCapacityPresentation(control).applied
  return capacityToCableY(
    capacity,
    control,
    baseline,
    QUEUE_CABLE_MAX_LIFT,
  )
}

export function getQueueMarkerPathGeometry(
  queueId: QueueId,
  control: NumericControlSnapshot,
): QueueMarkerPathGeometry {
  const geometry = QUEUE_TRAVERSALS[queueId]
  const occupancyTopY = appliedTopY(control, geometry.start.y)
  const occupancyLength = geometry.end.x - geometry.start.x +
    2 * Math.max(0, geometry.start.y - occupancyTopY)
  const transientStart = {
    x: geometry.start.x,
    y: geometry.start.y - TRANSIENT_OFFSET_Y,
  }
  const transientEnd = {
    x: geometry.end.x,
    y: geometry.end.y - TRANSIENT_OFFSET_Y,
  }
  const transientTopY = occupancyTopY - TRANSIENT_OFFSET_Y
  const transientCableLength = transientEnd.x - transientStart.x +
    2 * Math.max(0, transientStart.y - transientTopY)
  const prefixLength = polylineLength([...geometry.upstream, transientStart])
  const suffixLength = polylineLength([transientEnd, ...geometry.downstream])
  const totalLength = prefixLength + transientCableLength + suffixLength
  const handleCenterDistance = prefixLength + transientCableLength / 2
  const flowLength = Math.max(1, handleCenterDistance - HANDLE_CLEARANCE)
  const handoffStartDistance = Math.min(
    totalLength - 1,
    handleCenterDistance + HANDLE_CLEARANCE,
  )
  const handoffLength = Math.max(1, totalLength - handoffStartDistance)

  return {
    occupancyPath: buildQueueCablePath(
      geometry.start,
      geometry.end,
      occupancyTopY,
    ),
    transientPath: buildQueueTraversalPath(
      geometry.upstream,
      transientStart,
      transientEnd,
      transientTopY,
      geometry.downstream,
    ),
    occupancyLength,
    flowLength,
    handoffLength,
    flowEndRatio: flowLength / totalLength,
    handoffStartRatio: handoffStartDistance / totalLength,
  }
}

export function getHttpTraversalPath(): string {
  const [first, ...rest] = HTTP_TRAVERSAL_POINTS
  if (first === undefined) return ''
  return [
    `M${first.x} ${first.y}`,
    ...rest.map((point) => `L${point.x} ${point.y}`),
  ].join(' ')
}

export function getHttpTraversalLength(): number {
  return polylineLength(HTTP_TRAVERSAL_POINTS)
}
