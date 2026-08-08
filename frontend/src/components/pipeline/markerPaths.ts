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
  readonly flowPath: string
  readonly occupancyLength: number
  readonly flowLength: number
  readonly flowTopY: number
  readonly handleCenterY: number
}

export const FLOW_MARKER_RADIUS = 3.5
export const QUEUE_HANDLE_HALF_HEIGHT = 12
export const FLOW_HANDLE_GAP = 4

const FLOW_OFFSET_Y =
  QUEUE_HANDLE_HALF_HEIGHT + FLOW_MARKER_RADIUS + FLOW_HANDLE_GAP

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

function capacityY(
  capacity: number,
  control: NumericControlSnapshot,
  baseline: number,
): number {
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
  const capacity = getQueueCapacityPresentation(control)
  const occupancyTopY = capacityY(
    capacity.applied,
    control,
    geometry.start.y,
  )
  const handleCenterY = capacityY(
    capacity.candidate,
    control,
    geometry.start.y,
  )
  const occupancyLength = geometry.end.x - geometry.start.x +
    2 * Math.max(0, geometry.start.y - occupancyTopY)
  const flowStart = {
    x: geometry.start.x,
    y: geometry.start.y - FLOW_OFFSET_Y,
  }
  const flowEnd = {
    x: geometry.end.x,
    y: geometry.end.y - FLOW_OFFSET_Y,
  }
  const flowTopY = Math.min(occupancyTopY, handleCenterY) - FLOW_OFFSET_Y
  const flowCableLength = flowEnd.x - flowStart.x +
    2 * Math.max(0, flowStart.y - flowTopY)
  const prefixLength = polylineLength([...geometry.upstream, flowStart])
  const suffixLength = polylineLength([flowEnd, ...geometry.downstream])
  const flowLength = prefixLength + flowCableLength + suffixLength

  return {
    occupancyPath: buildQueueCablePath(
      geometry.start,
      geometry.end,
      occupancyTopY,
    ),
    flowPath: buildQueueTraversalPath(
      geometry.upstream,
      flowStart,
      flowEnd,
      flowTopY,
      geometry.downstream,
    ),
    occupancyLength,
    flowLength,
    flowTopY,
    handleCenterY,
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
