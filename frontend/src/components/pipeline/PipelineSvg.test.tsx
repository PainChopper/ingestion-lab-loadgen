import { act, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  QueueTelemetrySnapshot,
} from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import { QUEUE_CABLE_ENDPOINTS } from './geometry'
import {
  getFlowMarkerTarget,
  getQueueDepthFamilyTarget,
  MAX_PIPELINE_MARKERS,
} from './markerLifecycle'
import type { MarkerLifecycleSnapshot } from './markerLifecycle'
import { getQueueMarkerPathGeometry } from './markerPaths'
import { PipelineMarkers } from './PipelineMarkers'
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
  it('projects the unified marker pool onto applied queue paths', () => {
    const snapshot = activeSnapshot()
    const telemetry = markerTelemetryFromSnapshot(snapshot, false)
    const { container } = renderPipeline(snapshot)
    const cases = [
      { queue: snapshot.queue1, stage: 'queue1' as const },
      { queue: snapshot.queue2, stage: 'queue2' as const },
    ] as const

    for (const testCase of cases) {
      const queueGroup = container.querySelector(`#queue-${testCase.queue.id}`)!
      const cable = queueGroup.querySelector<SVGPathElement>('.pipeline-queue-cable')!
      const markers = [...container.querySelectorAll<SVGCircleElement>(
        `.pipeline-marker[data-marker-stage="${testCase.stage}"]`,
      )]
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
      expect(telemetry.stageTravelLengths[testCase.stage])
        .toBe(geometry.cableLength)
    }

    const activeFamilies = [...container.querySelectorAll<SVGCircleElement>(
      '.pipeline-marker[data-marker-state="active"]',
    )]
    expect(activeFamilies.length).toBeGreaterThanOrEqual(3)
    expect(activeFamilies.every((marker) => marker.dataset.familyId !== ''))
      .toBe(true)
  })

  it('keeps marker ownership outside QueueCable and removes request UI after apply', () => {
    const pending = activeSnapshot()
    const view = renderPipeline(pending)

    for (const queue of [pending.queue1, pending.queue2]) {
      const queueGroup = view.container.querySelector(`#queue-${queue.id}`)!
      const cable = queueGroup.querySelector('.pipeline-queue-cable')!
      const appliedLabel = queueGroup.querySelector('.pipeline-queue-capacity-applied')!
      const metrics = [...queueGroup.querySelectorAll('.pipeline-queue-metric')]
      const slider = queueGroup.querySelector('.pipeline-queue-handle')!
      const children = [...queueGroup.children]

      expect(queueGroup.querySelector('.pipeline-queue-requested-cable')).toBeNull()
      expect(queueGroup.querySelector('.pipeline-marker')).toBeNull()
      expect(children.indexOf(cable)).toBeLessThan(children.indexOf(appliedLabel))
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

  it('uses the arrived marker itself for TARGET outcome and has no helper slots', () => {
    const snapshot = activeSnapshot()
    const arrivingMarkers: MarkerLifecycleSnapshot = {
      revision: 1,
      reducedMotion: false,
      markers: [{
        slotId: 'pipeline-marker-1',
        familyId: 'transaction-family-1',
        state: 'retiring',
        stage: 'target',
        phase: 0.008,
        queued: false,
        outcome: null,
        outcomeVisible: false,
        pulseProgress: 0,
      }],
    }
    const view = render(
      <svg viewBox="0 0 1100 640">
        <PipelineMarkers snapshot={snapshot} markers={arrivingMarkers} />
      </svg>,
    )
    let marker = view.container.querySelector<SVGCircleElement>(
      '[data-family-id="transaction-family-1"]',
    )!

    expect(marker.dataset.markerOutcome).toBe('')
    expect(marker.classList).not.toContain('pipeline-marker--outcome')

    view.rerender(
      <svg viewBox="0 0 1100 640">
        <PipelineMarkers
          snapshot={snapshot}
          markers={{
            ...arrivingMarkers,
            revision: 2,
            markers: [{
              ...arrivingMarkers.markers[0],
              phase: 1,
              outcome: 'error',
              outcomeVisible: true,
              pulseProgress: 0.5,
            }],
          }}
        />
      </svg>,
    )
    marker = view.container.querySelector<SVGCircleElement>(
      '[data-family-id="transaction-family-1"]',
    )!

    expect(marker.dataset.markerOutcome).toBe('error')
    expect(marker.classList).toContain('pipeline-marker--outcome')
    expect(marker.classList).toContain('pipeline-marker--error')
    expect(Number(marker.getAttribute('r'))).toBeGreaterThan(4)
    expect(marker.style.filter).toContain('drop-shadow')
    expect(view.container.querySelector('.pipeline-target-outcome')).toBeNull()
    expect(view.container.querySelector('.pipeline-processing-slot')).toBeNull()
  })

  it('preserves mounted families at accepted requested TPS zero across a reader worker revision', () => {
    const readVisibleFamilies = (container: HTMLElement) => new Map(
      [...container.querySelectorAll<SVGCircleElement>(
        '.pipeline-marker:not([data-marker-state="inactive"])',
      )].map((marker) => [marker.dataset.familyId!, {
        state: marker.dataset.markerState,
        stage: marker.dataset.markerStage,
        phase: marker.dataset.markerPhase,
      }]),
    )
    const base = activeSnapshot()
    const moving: LoadgenSnapshot = {
      ...base,
      queue1: { ...base.queue1, throughputTps: 250_000 },
      queue2: { ...base.queue2, throughputTps: 250_000 },
    }
    const view = renderPipeline(moving)
    const beforeZero = readVisibleFamilies(view.container)
    const acceptedZero: LoadgenSnapshot = {
      ...moving,
      throttler: {
        ...moving.throttler,
        requestedTps: {
          ...moving.throttler.requestedTps,
          applied: 0,
          preview: null,
          pending: null,
        },
      },
      queue1: { ...moving.queue1, throughputTps: 0 },
      queue2: { ...moving.queue2, throughputTps: 0 },
    }
    view.rerender(
      <PipelineSvg
        snapshot={acceptedZero}
        selectedId={null}
        onSelect={vi.fn()}
        onWorkerCountChange={vi.fn()}
        onQueueCapacityChange={vi.fn()}
      />,
    )
    const atZero = readVisibleFamilies(view.container)

    expect(acceptedZero.throttler.requestedTps.applied).toBe(0)
    expect(acceptedZero.queue1.throughputTps).toBe(0)
    expect(acceptedZero.queue2.throughputTps).toBe(0)
    expect(atZero).toEqual(beforeZero)

    const currentWorkers = acceptedZero.reader.workers.applied ?? 0
    const nextWorkers = currentWorkers < acceptedZero.reader.workers.max
      ? currentWorkers + acceptedZero.reader.workers.step
      : currentWorkers - acceptedZero.reader.workers.step
    const afterWorkerChange: LoadgenSnapshot = {
      ...acceptedZero,
      reader: {
        ...acceptedZero.reader,
        workers: {
          ...acceptedZero.reader.workers,
          applied: nextWorkers,
          preview: null,
          pending: null,
        },
      },
    }
    view.rerender(
      <PipelineSvg
        snapshot={afterWorkerChange}
        selectedId={null}
        onSelect={vi.fn()}
        onWorkerCountChange={vi.fn()}
        onQueueCapacityChange={vi.fn()}
      />,
    )

    expect(afterWorkerChange.reader.workers.applied).not.toBe(currentWorkers)
    expect(readVisibleFamilies(view.container)).toEqual(atZero)
  })

  it('keeps one mounted renderer and stable flow families across telemetry revisions', () => {
    const withThroughput = (
      snapshot: LoadgenSnapshot,
      throughputTps: number,
    ): LoadgenSnapshot => ({
      ...snapshot,
      queue1: { ...snapshot.queue1, throughputTps },
      queue2: { ...snapshot.queue2, throughputTps },
    })
    const readActiveFlow = (container: HTMLElement) => new Map(
      [...container.querySelectorAll<SVGCircleElement>(
        '.pipeline-marker[data-marker-state="active"]',
      )].map((marker) => [marker.dataset.familyId!, {
        slotId: marker.dataset.markerId,
        stage: marker.dataset.markerStage,
        phase: marker.dataset.markerPhase,
      }]),
    )
    const base = activeSnapshot()
    let snapshot = withThroughput(base, 250_000)
    const view = renderPipeline(snapshot)
    const revisions = [125_000, 50_000, 250_000, 250_000, 1, 125_000, 250_000]

    for (const throughputTps of revisions) {
      const before = readActiveFlow(view.container)
      snapshot = withThroughput(base, throughputTps)
      view.rerender(
        <PipelineSvg
          snapshot={snapshot}
          selectedId={null}
          onSelect={vi.fn()}
          onWorkerCountChange={vi.fn()}
          onQueueCapacityChange={vi.fn()}
        />,
      )
      const after = readActiveFlow(view.container)
      const survivors = [...after].filter(([familyId]) => before.has(familyId))

      expect(after.size).toBe(
        getQueueDepthFamilyTarget(
          snapshot.queue1.depthBatches,
          snapshot.queue1.capacity.applied ?? 0,
        ) +
          getQueueDepthFamilyTarget(
            snapshot.queue2.depthBatches,
            snapshot.queue2.capacity.applied ?? 0,
          ) +
          getFlowMarkerTarget(throughputTps),
      )
      expect(survivors.length).toBeGreaterThan(0)
      expect(survivors).toHaveLength(Math.min(before.size, after.size))
      for (const [familyId, position] of survivors) {
        expect(position).toEqual(before.get(familyId))
      }
      expect(view.container.querySelectorAll('.pipeline-marker-layer'))
        .toHaveLength(1)
      const pool = [...view.container.querySelectorAll<SVGCircleElement>(
        '.pipeline-marker',
      )]
      expect(pool).toHaveLength(MAX_PIPELINE_MARKERS)
      expect(new Set(pool.map((marker) => marker.dataset.markerId)).size)
        .toBe(MAX_PIPELINE_MARKERS)
    }

  })

  it('advances mounted marker DOM and owns the repeated rAF lifecycle', () => {
    const callbacks = new Map<number, FrameRequestCallback>()
    let nextFrameId = 1
    const requestSpy = vi.spyOn(window, 'requestAnimationFrame')
      .mockImplementation((callback) => {
        const frameId = nextFrameId
        nextFrameId += 1
        callbacks.set(frameId, callback)
        return frameId
      })
    const cancelSpy = vi.spyOn(window, 'cancelAnimationFrame')
      .mockImplementation((frameId) => {
        callbacks.delete(frameId)
      })
    const nowSpy = vi.spyOn(performance, 'now').mockReturnValue(1_000)
    requestSpy.mockClear()
    cancelSpy.mockClear()

    try {
      const view = renderPipeline(activeSnapshot())
      const marker = view.container.querySelector<SVGCircleElement>(
        '.pipeline-marker[data-marker-state="active"]',
      )!
      const familyId = marker.dataset.familyId!
      const readPosition = () => {
        const current = view.container.querySelector<SVGCircleElement>(
          `[data-family-id="${familyId}"]`,
        )!
        return {
          stage: current.dataset.markerStage,
          phase: current.dataset.markerPhase,
        }
      }
      const runFrame = (frameId: number, time: number) => {
        const callback = callbacks.get(frameId)!
        callbacks.delete(frameId)
        act(() => callback(time))
      }

      const initial = readPosition()
      expect(requestSpy).toHaveBeenCalledTimes(1)
      runFrame(1, 1_100)
      const afterFirstFrame = readPosition()
      expect(afterFirstFrame).not.toEqual(initial)
      expect(requestSpy).toHaveBeenCalledTimes(2)

      runFrame(2, 1_200)
      expect(readPosition()).not.toEqual(afterFirstFrame)
      expect(requestSpy).toHaveBeenCalledTimes(3)

      view.unmount()
      expect(cancelSpy).toHaveBeenCalledWith(3)
      expect(callbacks.has(3)).toBe(false)
    } finally {
      nowSpy.mockRestore()
      cancelSpy.mockRestore()
      requestSpy.mockRestore()
    }
  })
})
