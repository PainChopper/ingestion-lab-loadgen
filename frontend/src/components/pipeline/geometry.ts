import type { QueueId } from '../../model/loadgen'
import type { PipelineOrientation } from './pipelineLayout'
import { getPortraitWorkerGridMetrics } from './workerActorLayout'

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
  readonly fixedHeight?: number
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

export interface PipelineQueueGeometry extends QueueCableEndpoints {
  readonly metrics: {
    readonly x: number
    readonly throughputY: number
    readonly depthY: number
    readonly waitingY: number
    readonly requestY: number
  }
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

export interface PipelineGeometryInput {
  readonly orientation: PipelineOrientation
  readonly landscapeContentWidth?: number
  readonly readerWorkers: number
  readonly senderWorkers: number
}

function landscapeGeometry(contentWidth: number) {
  const width = Math.max(PIPELINE_VIEW_BOX.width, Math.round(contentWidth))
  const extraWidth = width - PIPELINE_VIEW_BOX.width
  const delta = extraWidth / 3
  const throttlerOffset = delta
  const senderOffset = delta * 2
  const targetOffset = extraWidth
  const reader = ACTOR_GEOMETRY.reader
  const throttler = {
    bounds: {
      ...ACTOR_GEOMETRY.throttler.bounds,
      x: ACTOR_GEOMETRY.throttler.bounds.x + throttlerOffset,
    },
    ports: {
      input: {
        x: ACTOR_GEOMETRY.throttler.ports.input.x + throttlerOffset,
        y: FLOW_BASELINE,
      },
      output: {
        x: ACTOR_GEOMETRY.throttler.ports.output.x + throttlerOffset,
        y: FLOW_BASELINE,
      },
    },
    title: {
      x: ACTOR_GEOMETRY.throttler.title.x + throttlerOffset,
      y: ACTOR_GEOMETRY.throttler.title.y,
    },
    transform: { x: throttlerOffset, y: 0 },
  }
  const sender = {
    bounds: {
      ...ACTOR_GEOMETRY.sender.bounds,
      x: ACTOR_GEOMETRY.sender.bounds.x + senderOffset,
    },
    ports: {
      input: {
        x: ACTOR_GEOMETRY.sender.ports.input.x + senderOffset,
        y: FLOW_BASELINE,
      },
      output: {
        x: ACTOR_GEOMETRY.sender.ports.output.x + senderOffset,
        y: FLOW_BASELINE,
      },
    },
    title: {
      x: ACTOR_GEOMETRY.sender.title.x + senderOffset,
      y: ACTOR_GEOMETRY.sender.title.y,
    },
    controls: {
      ...ACTOR_GEOMETRY.sender.controls,
      x: ACTOR_GEOMETRY.sender.controls.x + senderOffset,
    },
    markerPoint: { x: 783 + senderOffset, y: 397 },
  }
  const target = {
    bounds: {
      ...ACTOR_GEOMETRY.target.bounds,
      x: ACTOR_GEOMETRY.target.bounds.x + targetOffset,
    },
    ports: {
      input: {
        x: ACTOR_GEOMETRY.target.ports.input.x + targetOffset,
        y: FLOW_BASELINE,
      },
    },
    title: {
      x: ACTOR_GEOMETRY.target.title.x + targetOffset,
      y: ACTOR_GEOMETRY.target.title.y,
    },
    center: { x: 1000 + targetOffset, y: 342 },
    labels: {
      caption: { x: 1000 + targetOffset, y: 407 },
      value: { x: 1000 + targetOffset, y: 431 },
      state: { x: 1000 + targetOffset, y: 456 },
    },
    markerPoint: { x: 962 + targetOffset, y: FLOW_BASELINE },
  }
  const queues: Record<QueueId, PipelineQueueGeometry> = {
    'reader-to-throttler': {
      start: reader.ports.output,
      end: throttler.ports.input,
      metrics: {
        x: (reader.ports.output.x + throttler.ports.input.x) / 2,
        throughputY: 507,
        depthY: 528,
        waitingY: 548,
        requestY: 566,
      },
    },
    'throttler-to-sender': {
      start: throttler.ports.output,
      end: sender.ports.input,
      metrics: {
        x: (throttler.ports.output.x + sender.ports.input.x) / 2,
        throughputY: 507,
        depthY: 528,
        waitingY: 548,
        requestY: 566,
      },
    },
  }

  return {
    orientation: 'landscape' as const,
    viewBox: {
      width,
      height: PIPELINE_VIEW_BOX.height,
      value: `0 0 ${width} ${PIPELINE_VIEW_BOX.height}`,
    },
    stationDelta: delta,
    actors: {
      reader: { ...reader, markerPoint: { x: 96, y: 392 } },
      throttler,
      sender,
      target,
    },
    queues,
    http: {
      start: sender.ports.output,
      end: target.ports.input,
      metrics: {
        x: (sender.ports.output.x + target.ports.input.x) / 2,
        statusY: 507,
        throughputY: 528,
        detailY: 549,
      },
    },
  }
}

function portraitGeometry(readerWorkers: number, senderWorkers: number) {
  const readerGrid = getPortraitWorkerGridMetrics('reader', readerWorkers)
  const senderGrid = getPortraitWorkerGridMetrics('sender', senderWorkers)
  const readerTop = 88
  const readerBottom = readerTop + readerGrid.height
  const throttlerInput = readerBottom + 247
  const throttlerOutput = throttlerInput + 193
  const senderTop = throttlerOutput + 222
  const senderBottom = senderTop + senderGrid.height
  const targetTop = senderBottom + 170
  const viewBoxHeight = 1160 + readerGrid.height + senderGrid.height
  const reader = {
    bounds: {
      x: 40,
      width: 400,
      bottom: readerBottom,
      rowHeight: 45,
      padding: 18,
    } satisfies WorkerActorBounds,
    ports: { output: { x: 240, y: readerBottom } satisfies Point },
    title: { x: 240, y: 34 } satisfies Point,
    controls: { x: 196, y: 48, width: 88, height: 30 },
    metrics: {
      primary: {
        x: 240,
        y: readerTop + readerGrid.gridBottom + 22,
      } satisfies Point,
      secondary: {
        x: 240,
        y: readerTop + readerGrid.gridBottom + 43,
      } satisfies Point,
    },
    markerPoint: { x: 240, y: (readerTop + readerBottom) / 2 },
  }
  const throttler = {
    bounds: {
      x: 165,
      y: throttlerInput,
      width: 150,
      height: 193,
    } satisfies FixedActorBounds,
    ports: {
      input: { x: 240, y: throttlerInput } satisfies Point,
      output: { x: 240, y: throttlerOutput } satisfies Point,
    },
    title: { x: 240, y: throttlerInput - 15 } satisfies Point,
    transform: { x: -190, y: throttlerInput - 282 } satisfies Point,
  }
  const sender = {
    bounds: {
      x: 40,
      width: 400,
      bottom: senderBottom,
      rowHeight: 45,
      padding: 18,
    } satisfies WorkerActorBounds,
    ports: {
      input: { x: 240, y: senderTop } satisfies Point,
      output: { x: 240, y: senderBottom } satisfies Point,
    },
    title: { x: 240, y: senderTop - 54 } satisfies Point,
    controls: { x: 196, y: senderTop - 40, width: 88, height: 30 },
    metrics: {
      primary: {
        x: 240,
        y: senderTop + senderGrid.gridBottom + 22,
      } satisfies Point,
      secondary: {
        x: 240,
        y: senderTop + senderGrid.gridBottom + 43,
      } satisfies Point,
    },
    markerPoint: { x: 240, y: (senderTop + senderBottom) / 2 },
  }
  const target = {
    bounds: {
      x: 120,
      y: targetTop,
      width: 240,
      height: 210,
    } satisfies FixedActorBounds,
    ports: { input: { x: 240, y: targetTop } satisfies Point },
    title: { x: 240, y: targetTop - 17 } satisfies Point,
    center: { x: 240, y: targetTop + 80 } satisfies Point,
    labels: {
      caption: { x: 240, y: targetTop + 145 } satisfies Point,
      value: { x: 240, y: targetTop + 169 } satisfies Point,
      state: { x: 240, y: targetTop + 194 } satisfies Point,
    },
    markerPoint: { x: 240, y: targetTop + 43 } satisfies Point,
  }
  const queues: Record<QueueId, PipelineQueueGeometry> = {
    'reader-to-throttler': {
      start: reader.ports.output,
      end: throttler.ports.input,
      metrics: {
        x: 350,
        throughputY: readerBottom + 60,
        depthY: readerBottom + 84,
        waitingY: readerBottom + 107,
        requestY: readerBottom + 130,
      },
    },
    'throttler-to-sender': {
      start: throttler.ports.output,
      end: sender.ports.input,
      metrics: {
        x: 350,
        throughputY: throttlerOutput + 42,
        depthY: throttlerOutput + 66,
        waitingY: throttlerOutput + 89,
        requestY: throttlerOutput + 112,
      },
    },
  }

  return {
    orientation: 'portrait' as const,
    viewBox: {
      width: 480,
      height: viewBoxHeight,
      value: `0 0 480 ${viewBoxHeight}`,
    },
    stationDelta: 0,
    actors: { reader, throttler, sender, target },
    queues,
    http: {
      start: sender.ports.output,
      end: target.ports.input,
      metrics: {
        x: 340,
        statusY: (senderBottom + targetTop) / 2 - 24,
        throughputY: (senderBottom + targetTop) / 2,
        detailY: (senderBottom + targetTop) / 2 + 24,
      },
    },
  }
}

export function createPipelineGeometry({
  orientation,
  landscapeContentWidth = PIPELINE_VIEW_BOX.width,
  readerWorkers,
  senderWorkers,
}: PipelineGeometryInput) {
  return orientation === 'portrait'
    ? portraitGeometry(readerWorkers, senderWorkers)
    : landscapeGeometry(landscapeContentWidth)
}

export type PipelineGeometry = ReturnType<typeof createPipelineGeometry>
