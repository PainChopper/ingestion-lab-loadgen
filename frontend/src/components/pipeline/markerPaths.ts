import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import type { Point } from './geometry'
import { ACTOR_GEOMETRY, QUEUE_CABLE_ENDPOINTS } from './geometry'
import type { MarkerStage } from './markerLifecycle'
import {
  getQueueCableGeometryPresentation,
} from './queueCableGeometry'
import {
  VALVE_APERTURE,
  VALVE_FLANGES,
  valvePassageCenterY,
} from './throttlerValve'

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

export interface ValveMarkerPathGeometry extends MarkerStagePathGeometry {
  readonly points: readonly Point[]
  readonly preAdmissionStopPhase: number
  readonly exitPhase: number
}

export const VALVE_WAITING_STOP_X = VALVE_FLANGES.left - 4

const FIXED_STAGE_POINTS: Readonly<
  Partial<Record<MarkerStage, readonly Point[]>>
> = Object.freeze({
  reader: [
    { x: 96, y: 392 },
    ACTOR_GEOMETRY.reader.ports.output,
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

function distanceAtPoint(points: readonly Point[], pointIndex: number): number {
  return polylineLength(points.slice(0, pointIndex + 1))
}

export function getValveMarkerPathGeometry(
  openingIndex: number,
): ValveMarkerPathGeometry {
  const passageY = valvePassageCenterY(openingIndex === 0 ? 1 : openingIndex)
  const points = Object.freeze([
    ACTOR_GEOMETRY.throttler.ports.input,
    { x: VALVE_WAITING_STOP_X, y: VALVE_APERTURE.centerY },
    { x: VALVE_FLANGES.left, y: VALVE_APERTURE.centerY },
    { x: VALVE_APERTURE.centerX - VALVE_APERTURE.radiusX, y: VALVE_APERTURE.centerY },
    { x: VALVE_APERTURE.centerX - 9, y: passageY },
    { x: VALVE_APERTURE.centerX + 9, y: passageY },
    { x: VALVE_APERTURE.centerX + VALVE_APERTURE.radiusX, y: VALVE_APERTURE.centerY },
    { x: VALVE_FLANGES.right, y: VALVE_APERTURE.centerY },
    ACTOR_GEOMETRY.throttler.ports.output,
  ])
  const length = polylineLength(points)
  return Object.freeze({
    path: pointsPath(points),
    length,
    start: points[0],
    end: points.at(-1) ?? points[0],
    points,
    preAdmissionStopPhase: distanceAtPoint(points, 1) / length,
    exitPhase: distanceAtPoint(points, 7) / length,
  })
}

export function pointAtValvePathPhase(
  geometry: ValveMarkerPathGeometry,
  phase: number,
): Point {
  let remaining = clampPhase(phase) * geometry.length
  for (let index = 1; index < geometry.points.length; index += 1) {
    const left = geometry.points[index - 1]
    const right = geometry.points[index]
    const segmentLength = pointDistance(left, right)
    if (remaining <= segmentLength) {
      const progress = segmentLength === 0 ? 0 : remaining / segmentLength
      return {
        x: left.x + (right.x - left.x) * progress,
        y: left.y + (right.y - left.y) * progress,
      }
    }
    remaining -= segmentLength
  }
  return geometry.end
}

function clampPhase(phase: number): number {
  return Math.min(1, Math.max(0, phase))
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
  valveOpeningIndex = 11,
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

  if (stage === 'throttler') {
    return getValveMarkerPathGeometry(valveOpeningIndex)
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
