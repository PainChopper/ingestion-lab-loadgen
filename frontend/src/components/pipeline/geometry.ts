import type { QueueId } from '../../model/loadgen'

export interface Point {
  readonly x: number
  readonly y: number
}

export interface FixedActorBounds {
  readonly x: number
  readonly y: number
  readonly width: number
  readonly height: number
}

export interface WorkerActorBounds {
  readonly x: number
  readonly width: number
  readonly bottom: number
  readonly rowHeight: number
  readonly padding: number
}

export const PIPELINE_VIEW_BOX = Object.freeze({
  width: 1120,
  height: 650,
})

export const PIPELINE_VIEW_BOX_VALUE =
  `0 0 ${PIPELINE_VIEW_BOX.width} ${PIPELINE_VIEW_BOX.height}`

export const FLOW_BASELINE = 415
export const ACTOR_BOTTOM = 475

export const ACTOR_GEOMETRY = Object.freeze({
  reader: {
    bounds: {
      x: 30,
      width: 120,
      bottom: ACTOR_BOTTOM,
      rowHeight: 39,
      padding: 16,
    } satisfies WorkerActorBounds,
    ports: { output: { x: 150, y: FLOW_BASELINE } satisfies Point },
    title: { x: 90, y: 38 } satisfies Point,
    controls: { x: 46, y: 58, width: 88, height: 30 },
  },
  throttler: {
    bounds: {
      x: 355,
      y: 282,
      width: 150,
      height: 193,
    } satisfies FixedActorBounds,
    ports: {
      input: { x: 355, y: FLOW_BASELINE } satisfies Point,
      output: { x: 505, y: FLOW_BASELINE } satisfies Point,
    },
    title: { x: 430, y: 38 } satisfies Point,
  },
  sender: {
    bounds: {
      x: 720,
      width: 120,
      bottom: ACTOR_BOTTOM,
      rowHeight: 39,
      padding: 16,
    } satisfies WorkerActorBounds,
    ports: {
      input: { x: 720, y: FLOW_BASELINE } satisfies Point,
      output: { x: 840, y: FLOW_BASELINE } satisfies Point,
    },
    title: { x: 780, y: 38 } satisfies Point,
    controls: { x: 736, y: 58, width: 88, height: 30 },
  },
  target: {
    bounds: {
      x: 930,
      y: 245,
      width: 140,
      height: 230,
    } satisfies FixedActorBounds,
    ports: { input: { x: 930, y: FLOW_BASELINE } satisfies Point },
    title: { x: 1000, y: 38 } satisfies Point,
  },
})

export interface QueueCableEndpoints {
  readonly start: Point
  readonly end: Point
}

export const QUEUE_CABLE_ENDPOINTS: Readonly<
  Record<QueueId, QueueCableEndpoints>
> = Object.freeze({
  'reader-to-throttler': {
    start: ACTOR_GEOMETRY.reader.ports.output,
    end: ACTOR_GEOMETRY.throttler.ports.input,
  },
  'throttler-to-sender': {
    start: ACTOR_GEOMETRY.throttler.ports.output,
    end: ACTOR_GEOMETRY.sender.ports.input,
  },
})
