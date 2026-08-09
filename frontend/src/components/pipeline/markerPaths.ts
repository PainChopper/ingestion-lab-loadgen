import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import type { Point } from './geometry'
import { ACTOR_GEOMETRY, QUEUE_CABLE_ENDPOINTS } from './geometry'
import {
  getQueueCableGeometryPresentation,
} from './queueCableGeometry'

export interface QueueMarkerPathGeometry {
  readonly cablePath: string
  readonly cableLength: number
  readonly cableY: number
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

export function getQueueMarkerPathGeometry(
  queueId: QueueId,
  control: NumericControlSnapshot,
): QueueMarkerPathGeometry {
  const endpoints = QUEUE_CABLE_ENDPOINTS[queueId]
  const geometry = getQueueCableGeometryPresentation(
    control,
    endpoints.start,
    endpoints.end,
  )

  return {
    cablePath: geometry.cablePath,
    cableLength: geometry.markerPathLength,
    cableY: geometry.cableY,
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
