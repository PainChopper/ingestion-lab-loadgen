import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import type { Point } from './geometry'
import { ACTOR_GEOMETRY, QUEUE_CABLE_ENDPOINTS } from './geometry'
import type { MarkerStage } from './markerLifecycle'
import {
  getQueueCableGeometryPresentation,
} from './queueCableGeometry'

export interface QueueMarkerPathGeometry {
  readonly cablePath: string
  readonly cableLength: number
  readonly cableY: number
}

export interface MarkerStagePathGeometry {
  readonly path: string
  readonly length: number
  readonly start: Point
  readonly end: Point
}

const FIXED_STAGE_POINTS: Readonly<
  Partial<Record<MarkerStage, readonly Point[]>>
> = Object.freeze({
  reader: [
    { x: 96, y: 392 },
    ACTOR_GEOMETRY.reader.ports.output,
  ],
  throttler: [
    ACTOR_GEOMETRY.throttler.ports.input,
    { x: 384, y: 398 },
    ACTOR_GEOMETRY.throttler.ports.output,
  ],
  sender: [
    ACTOR_GEOMETRY.sender.ports.input,
    { x: 783, y: 397 },
    ACTOR_GEOMETRY.sender.ports.output,
  ],
  http: [
    ACTOR_GEOMETRY.sender.ports.output,
    ACTOR_GEOMETRY.target.ports.input,
  ],
  target: [
    ACTOR_GEOMETRY.target.ports.input,
    { x: 962, y: 415 },
  ],
})

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

function pointsPath(points: readonly Point[]): string {
  const [first, ...rest] = points
  if (first === undefined) return ''
  return [
    `M${first.x} ${first.y}`,
    ...rest.map((point) => `L${point.x} ${point.y}`),
  ].join(' ')
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
  return pointsPath(FIXED_STAGE_POINTS.http ?? [])
}

export function getHttpTraversalLength(): number {
  return polylineLength(FIXED_STAGE_POINTS.http ?? [])
}

export function getMarkerStagePathGeometry(
  stage: MarkerStage,
  queue1Control: NumericControlSnapshot,
  queue2Control: NumericControlSnapshot,
): MarkerStagePathGeometry {
  if (stage === 'queue1' || stage === 'queue2') {
    const queueId: QueueId = stage === 'queue1'
      ? 'reader-to-throttler'
      : 'throttler-to-sender'
    const queue = getQueueMarkerPathGeometry(
      queueId,
      stage === 'queue1' ? queue1Control : queue2Control,
    )
    const endpoints = QUEUE_CABLE_ENDPOINTS[queueId]
    return {
      path: queue.cablePath,
      length: queue.cableLength,
      start: endpoints.start,
      end: endpoints.end,
    }
  }

  const points = FIXED_STAGE_POINTS[stage] ?? []
  const start = points[0] ?? { x: 0, y: 0 }
  const end = points.at(-1) ?? start
  return {
    path: pointsPath(points),
    length: polylineLength(points),
    start,
    end,
  }
}
