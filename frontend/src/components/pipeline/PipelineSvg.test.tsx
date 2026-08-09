import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  QueueTelemetrySnapshot,
} from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import { QUEUE_CABLE_ENDPOINTS } from './geometry'
import { getQueueMarkerPathGeometry } from './markerPaths'
import { PipelineSvg } from './PipelineSvg'
import { markerTelemetryFromSnapshot } from './usePipelineMarkerLifecycle'

function activeQueue<TQueue extends QueueTelemetrySnapshot>(
  queue: TQueue,
  applied: number,
  candidate: number | null,
): TQueue {
  return {
    ...queue,
    depthBatches: Math.min(applied, 4),
    throughputTps: 50_000,
    inputTps: 50_000,
    outputTps: 50_000,
    inputTransactionsPerSecond: 50_000,
    outputTransactionsPerSecond: 50_000,
    inputBatchesPerSecond: 10,
    outputBatchesPerSecond: 10,
    enqueuedBatchesTotal: 20,
    dequeuedBatchesTotal: 18,
    capacity: {
      ...queue.capacity,
      applied,
      preview: candidate,
      pending: candidate,
    },
  }
}

function activeSnapshot(): LoadgenSnapshot {
  const adapter = new SimulationAdapter()
  const base = adapter.getSnapshot()
  adapter.dispose()
  const telemetry: LoadgenTelemetrySnapshot = {
    ...base,
    runState: 'running',
    reader: { ...base.reader, state: 'running' },
    throttler: { ...base.throttler, state: 'running' },
    sender: { ...base.sender, state: 'running' },
    queue1: activeQueue(base.queue1, 10, 0),
    queue2: activeQueue(base.queue2, 160, 50),
  }
  return new QueueFlowStateDeriver().derive(telemetry, 0)
}

function renderPipeline(snapshot: LoadgenSnapshot) {
  return render(
    <PipelineSvg
      snapshot={snapshot}
      selectedId={null}
      onSelect={vi.fn()}
      onWorkerCountChange={vi.fn()}
      onQueueCapacityChange={vi.fn()}
    />,
  )
}

describe('PipelineSvg marker wiring', () => {
  it('binds real marker CSS and lifecycle travel to applied queue paths', () => {
    const snapshot = activeSnapshot()
    const telemetry = markerTelemetryFromSnapshot(snapshot, false)
    const { container } = renderPipeline(snapshot)
    const cases = [
      { queue: snapshot.queue1, telemetry: telemetry.queue1 },
      { queue: snapshot.queue2, telemetry: telemetry.queue2 },
    ] as const

    for (const testCase of cases) {
      const queueGroup = container.querySelector(`#queue-${testCase.queue.id}`)!
      const cable = queueGroup.querySelector<SVGPathElement>('.pipeline-queue-cable')!
      const markers = [...queueGroup.querySelectorAll<SVGCircleElement>('.pipeline-marker')]
      const path = cable.getAttribute('d')!
      const geometry = getQueueMarkerPathGeometry(
        testCase.queue.id,
        testCase.queue.capacity,
      )
      const endpoints = QUEUE_CABLE_ENDPOINTS[testCase.queue.id]

      expect(path).toBe(geometry.cablePath)
      expect(path.startsWith(`M${endpoints.start.x} ${endpoints.start.y}`))
        .toBe(true)
      expect(path.endsWith(`H${endpoints.end.x}`)).toBe(true)
      expect(markers.length).toBeGreaterThan(0)
      expect(markers.every((item) =>
        item.style.offsetPath === `path("${path}")`,
      )).toBe(true)
      expect(getComputedStyle(markers[0]).offsetPath).toBe(`path("${path}")`)
      expect(testCase.telemetry.occupancyTravelLength).toBe(
        geometry.cableLength,
      )
      expect(testCase.telemetry.flowTravelLength).toBe(geometry.cableLength)
    }
  })

  it('keeps cable-marker-label-slider order and removes request UI after apply', () => {
    const pending = activeSnapshot()
    const view = renderPipeline(pending)

    for (const queue of [pending.queue1, pending.queue2]) {
      const queueGroup = view.container.querySelector(`#queue-${queue.id}`)!
      const cable = queueGroup.querySelector('.pipeline-queue-cable')!
      const markerGroup = queueGroup.querySelector('.pipeline-queue-markers')!
      const appliedLabel = queueGroup.querySelector('.pipeline-queue-capacity-applied')!
      const metrics = [...queueGroup.querySelectorAll('.pipeline-queue-metric')]
      const slider = queueGroup.querySelector('.pipeline-queue-handle')!
      const children = [...queueGroup.children]

      expect(queueGroup.querySelector('.pipeline-queue-requested-cable'))
        .not.toBeNull()
      expect(queueGroup.querySelector('.pipeline-marker--occupancy'))
        .not.toBeNull()
      expect(queueGroup.querySelector('.pipeline-marker--flow')).not.toBeNull()
      expect(children.indexOf(cable)).toBeLessThan(children.indexOf(markerGroup))
      expect(children.indexOf(markerGroup)).toBeLessThan(children.indexOf(appliedLabel))
      expect(children.indexOf(appliedLabel)).toBeLessThan(
        children.indexOf(metrics[0]),
      )
      expect(children.indexOf(metrics.at(-1)!)).toBeLessThan(
        children.indexOf(slider),
      )
    }

    const applied = {
      ...pending,
      queue1: activeQueue(pending.queue1, 0, null),
      queue2: activeQueue(pending.queue2, 50, null),
    }
    view.rerender(
      <PipelineSvg
        snapshot={applied}
        selectedId={null}
        onSelect={vi.fn()}
        onWorkerCountChange={vi.fn()}
        onQueueCapacityChange={vi.fn()}
      />,
    )

    for (const queue of [applied.queue1, applied.queue2]) {
      const queueGroup = view.container.querySelector(`#queue-${queue.id}`)!
      expect(queueGroup.querySelector('.pipeline-queue-requested-cable')).toBeNull()
      expect(queueGroup.querySelector('.pipeline-queue-capacity-applied')).toBeNull()
      expect(queueGroup.querySelector('.pipeline-queue-capacity-status')).toBeNull()
    }
  })
})
