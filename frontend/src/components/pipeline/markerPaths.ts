import type { NumericControlSnapshot, QueueId } from '../../model/loadgen'
import {
  createPipelineGeometry,
  type PipelineGeometry,
  type Point,
} from './geometry'
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

const DEFAULT_PIPELINE_GEOMETRY = createPipelineGeometry({
  orientation: 'landscape',
  readerWorkers: 1,
  senderWorkers: 1,
})

function fixedStagePoints(geometry: PipelineGeometry): Readonly<
  Partial<Record<MarkerStage, readonly Point[]>>
> {
  return {
    reader: [
      geometry.actors.reader.markerPoint,
      geometry.actors.reader.ports.output,
    ],
    sender: [
      geometry.actors.sender.ports.input,
      geometry.actors.sender.markerPoint,
      geometry.actors.sender.ports.output,
    ],
    http: [geometry.http.start, geometry.http.end],
    target: [
      geometry.actors.target.ports.input,
      geometry.actors.target.markerPoint,
    ],
  }
}

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
  geometry: PipelineGeometry = DEFAULT_PIPELINE_GEOMETRY,
): ValveMarkerPathGeometry {
  const passageY = valvePassageCenterY(openingIndex === 0 ? 1 : openingIndex)
  const transform = geometry.actors.throttler.transform
  if (geometry.orientation === 'portrait') {
    const translate = (x: number, y: number): Point => ({
      x: x + transform.x,
      y: y + transform.y,
    })
    const points = Object.freeze([
      geometry.actors.throttler.ports.input,
      translate(382, 282),
      translate(382, 415),
      translate(397, 415),
      translate(401, 415),
      translate(415, 415),
      translate(421, passageY),
      translate(439, passageY),
      translate(445, 415),
      translate(459, 415),
      translate(478, 415),
      translate(478, 475),
      geometry.actors.throttler.ports.output,
    ])
    const length = polylineLength(points)
    return Object.freeze({
      path: pointsPath(points),
      length,
      start: points[0],
      end: points.at(-1) ?? points[0],
      points,
      preAdmissionStopPhase: distanceAtPoint(points, 3) / length,
      exitPhase: distanceAtPoint(points, 9) / length,
    })
  }

  const points = Object.freeze([
    geometry.actors.throttler.ports.input,
    { x: VALVE_WAITING_STOP_X + transform.x, y: VALVE_APERTURE.centerY },
    { x: VALVE_FLANGES.left + transform.x, y: VALVE_APERTURE.centerY },
    { x: VALVE_APERTURE.centerX - VALVE_APERTURE.radiusX + transform.x, y: VALVE_APERTURE.centerY },
    { x: VALVE_APERTURE.centerX - 9 + transform.x, y: passageY },
    { x: VALVE_APERTURE.centerX + 9 + transform.x, y: passageY },
    { x: VALVE_APERTURE.centerX + VALVE_APERTURE.radiusX + transform.x, y: VALVE_APERTURE.centerY },
    { x: VALVE_FLANGES.right + transform.x, y: VALVE_APERTURE.centerY },
    geometry.actors.throttler.ports.output,
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
  geometry: PipelineGeometry = DEFAULT_PIPELINE_GEOMETRY,
): QueueMarkerPathGeometry {
  const endpoints = geometry.queues[queueId]
  const presentationGeometry = getQueueCableGeometryPresentation(
    control,
    endpoints.start,
    endpoints.end,
    null,
    geometry.orientation,
  )

  return {
    cablePath: presentationGeometry.cablePath,
    cableLength: presentationGeometry.markerPathLength,
    cableY: presentationGeometry.cableY,
  }
}

export function getHttpTraversalPath(
  geometry: PipelineGeometry = DEFAULT_PIPELINE_GEOMETRY,
): string {
  const points = fixedStagePoints(geometry)
  return pointsPath(points.http ?? [])
}

export function getHttpTraversalLength(
  geometry: PipelineGeometry = DEFAULT_PIPELINE_GEOMETRY,
): number {
  const points = fixedStagePoints(geometry)
  return polylineLength(points.http ?? [])
}

export function getMarkerStagePathGeometry(
  stage: MarkerStage,
  queue1Control: NumericControlSnapshot,
  queue2Control: NumericControlSnapshot,
  valveOpeningIndex = 11,
  geometry: PipelineGeometry = DEFAULT_PIPELINE_GEOMETRY,
): MarkerStagePathGeometry {
  if (stage === 'queue1' || stage === 'queue2') {
    const queueId: QueueId = stage === 'queue1'
      ? 'reader-to-throttler'
      : 'throttler-to-sender'
    const queue = getQueueMarkerPathGeometry(
      queueId,
      stage === 'queue1' ? queue1Control : queue2Control,
      geometry,
    )
    const endpoints = geometry.queues[queueId]
    return {
      path: queue.cablePath,
      length: queue.cableLength,
      start: endpoints.start,
      end: endpoints.end,
    }
  }

  if (stage === 'throttler') {
    return getValveMarkerPathGeometry(valveOpeningIndex, geometry)
  }

  const stagePoints = fixedStagePoints(geometry)
  const points = stagePoints[stage] ?? []
  const start = points[0] ?? { x: 0, y: 0 }
  const end = points.at(-1) ?? start
  return {
    path: pointsPath(points),
    length: polylineLength(points),
    start,
    end,
  }
}
