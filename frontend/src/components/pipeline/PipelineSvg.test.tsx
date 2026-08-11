/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { act, render } from '@testing-library/react'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  QueueTelemetrySnapshot,
} from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import {
  createPipelineGeometry,
  QUEUE_CABLE_ENDPOINTS,
} from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
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
import { normalizedWorkerCount } from './workerActorLayout'

const styleElement = document.createElement('style')
const pipelineStyles = readFileSync(
  resolve(process.cwd(), 'src/components/pipeline/PipelineSvg.css'),
  'utf8',
)

beforeAll(() => {
  styleElement.textContent = pipelineStyles
  document.head.append(styleElement)
})

describe('responsive pipeline geometry', () => {
  it.each([
    { observed: 900, width: 1120 },
    { observed: 1120, width: 1120 },
    { observed: 1440, width: 1440 },
    { observed: 1920, width: 1920 },
  ])('distributes landscape stations at $observed px', ({ observed, width }) => {
    const geometry = createPipelineGeometry({
      orientation: 'landscape',
      landscapeContentWidth: observed,
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const delta = (width - 1120) / 3

    expect(geometry.viewBox).toEqual({
      width,
      height: 650,
      value: `0 0 ${width} 650`,
    })
    expect(geometry.stationDelta).toBe(delta)
    expect(geometry.actors.reader.bounds.width).toBe(120)
    expect(geometry.actors.throttler.bounds.width).toBe(150)
    expect(geometry.actors.sender.bounds.width).toBe(120)
    expect(geometry.actors.target.bounds.width).toBe(140)
    expect(width - (
      geometry.actors.target.bounds.x + geometry.actors.target.bounds.width
    )).toBe(50)
    expect(
      geometry.queues['reader-to-throttler'].end.x -
      geometry.queues['reader-to-throttler'].start.x,
    ).toBeCloseTo(205 + delta, 10)
    expect(
      geometry.queues['throttler-to-sender'].end.x -
      geometry.queues['throttler-to-sender'].start.x,
    ).toBeCloseTo(215 + delta, 10)
    expect(geometry.http.end.x - geometry.http.start.x)
      .toBeCloseTo(90 + delta, 10)
  })

  it.each([
    { reader: 1, sender: 1, readerHeight: 113, senderHeight: 113 },
    { reader: 7, sender: 1, readerHeight: 158, senderHeight: 113 },
    { reader: 7, sender: 7, readerHeight: 158, senderHeight: 158 },
    { reader: 7, sender: 8, readerHeight: 158, senderHeight: 96 },
    { reader: 7, sender: 16, readerHeight: 158, senderHeight: 124 },
    { reader: 7, sender: 32, readerHeight: 158, senderHeight: 180 },
  ])(
    'derives portrait stages for Reader $reader and Sender $sender',
    ({ reader, sender, readerHeight, senderHeight }) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: reader,
        senderWorkers: sender,
      })
      const readerBottom = 88 + readerHeight
      const throttlerInput = readerBottom + 247
      const throttlerOutput = throttlerInput + 193
      const senderTop = throttlerOutput + 222
      const senderBottom = senderTop + senderHeight
      const targetTop = senderBottom + 170

      expect(geometry.actors.reader.ports.output.y).toBe(readerBottom)
      expect(geometry.actors.throttler.ports.input.y).toBe(throttlerInput)
      expect(geometry.actors.throttler.ports.output.y).toBe(throttlerOutput)
      expect(geometry.actors.sender.ports.input.y).toBe(senderTop)
      expect(geometry.actors.sender.ports.output.y).toBe(senderBottom)
      expect(geometry.actors.target.ports.input.y).toBe(targetTop)
      expect(geometry.viewBox.height)
        .toBe(1160 + readerHeight + senderHeight)
      expect(geometry.queues['reader-to-throttler'].metrics.throughputY)
        .toBe(readerBottom + 60)
      expect(geometry.queues['throttler-to-sender'].metrics.throughputY)
        .toBe(throttlerOutput + 42)
    },
  )
})

afterAll(() => {
  styleElement.remove()
})

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

function renderPipeline(
  snapshot: LoadgenSnapshot,
  orientation: PipelineOrientation = 'landscape',
  landscapeContentWidth = 1120,
) {
  const geometry = createPipelineGeometry({
    orientation,
    landscapeContentWidth,
    readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
    senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
  })
  return render(
    <PipelineSvg
      snapshot={snapshot}
      selectedId={null}
      onSelect={vi.fn()}
      onWorkerCountChange={vi.fn()}
      onQueueCapacityChange={vi.fn()}
      requestedTpsPreview={null}
      onRequestedTpsPreviewChange={vi.fn()}
      onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
      onInstallationModeChange={vi.fn().mockResolvedValue(true)}
      orientation={orientation}
      geometry={geometry}
    />,
  )
}

describe('PipelineSvg marker wiring', () => {
  it.each(['landscape', 'portrait'] as const)(
    'shows Reader actual rate, capacity, and downstream limitation in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        reader: {
          ...base.reader,
          readTps: 50_000,
          configuredCapacityTps: 350_000,
          limitationReason: 'downstream-backpressure',
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const reader = view.container.querySelector('#reader-actor')

      expect(reader?.textContent).toContain('Read 50,000 tx/s')
      expect(reader?.textContent).toContain('Capacity 350,000 tx/s')
      expect(reader?.textContent).toContain('Downstream limited')
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'shows applied target failures and rolling failed TPS in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        target: {
          ...base.target,
          acceptedTps: 245_000,
          rejectedTps: 5_000,
          errorRatePercent: {
            ...base.target.errorRatePercent,
            applied: 2,
            preview: 7,
            pending: 7,
          },
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const target = view.container.querySelector('#target-actor')!
      const geometry = createPipelineGeometry({
        orientation,
        readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
        senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
      })
      const boundsBottom = geometry.actors.target.bounds.y +
        geometry.actors.target.bounds.height

      expect(target.textContent).toContain('245,000 tx/s')
      expect(target.textContent).toContain(
        '2% 503 rate · 5,000 rejected tx/s',
      )
      expect(target.textContent).not.toContain('7% 503 rate')
      expect(target.textContent).toContain('connected')
      expect(geometry.actors.target.labels.state.y).toBeLessThan(boundsBottom)
    },
  )

  it('preserves unknown and measured zero target failure telemetry', () => {
    const base = activeSnapshot()
    const cases = [
      { applied: null, rejectedTps: null, expected: '— 503 rate · — rejected tx/s' },
      { applied: 0, rejectedTps: 0, expected: '0% 503 rate · 0 rejected tx/s' },
    ] as const

    for (const testCase of cases) {
      const snapshot: LoadgenSnapshot = {
        ...base,
        target: {
          ...base.target,
          rejectedTps: testCase.rejectedTps,
          errorRatePercent: {
            ...base.target.errorRatePercent,
            applied: testCase.applied,
            preview: 2,
            pending: 2,
          },
        },
      }
      const view = renderPipeline(snapshot)
      expect(view.container.querySelector('#target-actor')?.textContent)
        .toContain(testCase.expected)
      view.unmount()
    }
  })

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
        requestedTpsPreview={null}
        onRequestedTpsPreviewChange={vi.fn()}
        onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
        onInstallationModeChange={vi.fn().mockResolvedValue(true)}
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
      motionElapsedMs: 0,
      valveOpeningIndex: 5,
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
        requestedTpsPreview={null}
        onRequestedTpsPreviewChange={vi.fn()}
        onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
        onInstallationModeChange={vi.fn().mockResolvedValue(true)}
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
        requestedTpsPreview={null}
        onRequestedTpsPreviewChange={vi.fn()}
        onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
        onInstallationModeChange={vi.fn().mockResolvedValue(true)}
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
          requestedTpsPreview={null}
          onRequestedTpsPreviewChange={vi.fn()}
          onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
          onInstallationModeChange={vi.fn().mockResolvedValue(true)}
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

  it('bounds waiting backlog to eight stable FIFO families with deterministic motion', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      queue1: {
        ...base.queue1,
        depthBatches: 12,
        capacity: {
          ...base.queue1.capacity,
          applied: 12,
          preview: 0,
          pending: 0,
        },
      },
      throttler: {
        ...base.throttler,
        requestedTps: {
          ...base.throttler.requestedTps,
          applied: 0,
          preview: 250_000,
          pending: 225_000,
        },
      },
    }
    const waiting = Array.from({ length: 10 }, (_, index) => ({
      slotId: `pipeline-marker-${index + 1}`,
      familyId: `transaction-family-${index + 1}`,
      state: 'active' as const,
      stage: 'throttler' as const,
      phase: 0.3 - index * 0.01,
      queued: true,
      outcome: null,
      outcomeVisible: false,
      pulseProgress: 0,
    }))
    const markerSnapshot: MarkerLifecycleSnapshot = {
      revision: 1,
      reducedMotion: false,
      motionElapsedMs: 0,
      valveOpeningIndex: 0,
      markers: waiting,
    }
    const view = render(
      <svg><PipelineMarkers snapshot={snapshot} markers={markerSnapshot} /></svg>,
    )
    const visibleWaiting = () => [...view.container.querySelectorAll<SVGCircleElement>(
      '[data-marker-stage="throttler"][visibility="visible"]',
    )]
    const initial = visibleWaiting().map((marker) => ({
      familyId: marker.dataset.familyId,
      x: marker.dataset.markerJitterX,
      y: marker.dataset.markerJitterY,
    }))
    expect(initial).toHaveLength(8)
    expect(initial.map(({ familyId }) => familyId)).toEqual(
      waiting.slice(0, 8).map(({ familyId }) => familyId),
    )
    expect(view.container.querySelectorAll('[data-marker-mask="aperture-and-body"]'))
      .toHaveLength(10)

    view.rerender(
      <svg>
        <PipelineMarkers
          snapshot={snapshot}
          markers={{ ...markerSnapshot, revision: 2, motionElapsedMs: 500 }}
        />
      </svg>,
    )
    const moved = visibleWaiting().map((marker) => ({
      familyId: marker.dataset.familyId,
      x: marker.dataset.markerJitterX,
      y: marker.dataset.markerJitterY,
    }))
    expect(moved.map(({ familyId }) => familyId))
      .toEqual(initial.map(({ familyId }) => familyId))
    expect(moved.some((marker, index) =>
      marker.x !== initial[index].x || marker.y !== initial[index].y
    )).toBe(true)

    const withoutCandidate: LoadgenSnapshot = {
      ...snapshot,
      throttler: {
        ...snapshot.throttler,
        requestedTps: {
          ...snapshot.throttler.requestedTps,
          preview: null,
          pending: null,
        },
      },
    }
    view.rerender(
      <svg>
        <PipelineMarkers
          snapshot={withoutCandidate}
          markers={{ ...markerSnapshot, revision: 3, motionElapsedMs: 500 }}
        />
      </svg>,
    )
    expect(visibleWaiting().map((marker) => marker.dataset.familyId))
      .toEqual(initial.map(({ familyId }) => familyId))

    view.rerender(
      <svg>
        <PipelineMarkers
          snapshot={{
            ...withoutCandidate,
            queue1: { ...withoutCandidate.queue1, depthBatches: 3 },
          }}
          markers={{ ...markerSnapshot, revision: 4, motionElapsedMs: 500 }}
        />
      </svg>,
    )
    expect(visibleWaiting().map((marker) => marker.dataset.familyId))
      .toEqual(waiting.slice(0, 3).map(({ familyId }) => familyId))

    view.rerender(
      <svg>
        <PipelineMarkers
          snapshot={snapshot}
          markers={{
            ...markerSnapshot,
            revision: 5,
            reducedMotion: true,
            motionElapsedMs: 1_000,
          }}
        />
      </svg>,
    )
    expect(visibleWaiting()).toHaveLength(8)
    expect(visibleWaiting().every((marker) =>
      marker.dataset.markerJitterX === '0' &&
      marker.dataset.markerJitterY === '0'
    )).toBe(true)
  })

  it('keeps one uniform vacuum and an empty green q2 projection at drain end', () => {
    const base = activeSnapshot()
    const drained: LoadgenSnapshot = {
      ...base,
      throttler: {
        ...base.throttler,
        requestedTps: {
          ...base.throttler.requestedTps,
          applied: 0,
          preview: 250_000,
          pending: 225_000,
        },
      },
      queue1: {
        ...base.queue1,
        depthBatches: 0,
        throughputTps: 0,
        flowState: 'normal',
        displayedPressure: 0,
      },
      queue2: {
        ...base.queue2,
        depthBatches: 0,
        throughputTps: 0,
        flowState: 'normal',
        displayedPressure: 0,
      },
    }
    const view = renderPipeline(drained)
    const vacuum = view.container.querySelector<SVGEllipseElement>(
      '.pipeline-valve-vacuum',
    )!
    expect(vacuum.dataset.vacuumFill).toBe('uniform')
    expect(vacuum.getAttribute('fill')).toBe('#03111f')
    expect(view.container.querySelectorAll(
      '.pipeline-marker[data-marker-stage="queue2"][visibility="visible"]',
    )).toHaveLength(0)
    const q2 = view.container.querySelector<SVGGElement>(
      '#queue-throttler-to-sender',
    )!
    expect(q2.style.getPropertyValue('--pipeline-queue-pressure-color'))
      .toBe('#79d957')
  })

  it('projects rendezvous pressure without occupancy markers', () => {
    const base = activeSnapshot()
    const rendezvous: LoadgenSnapshot = {
      ...base,
      queue1: {
        ...base.queue1,
        depthBatches: 0,
        throughputTps: 0,
        blockedSenders: 1,
        oldestBlockedSenderMs: 10,
        displayedPressure: 1,
        flowState: 'backpressure',
        capacity: {
          ...base.queue1.capacity,
          applied: 0,
          preview: 12,
          pending: 12,
        },
      },
      queue2: {
        ...base.queue2,
        depthBatches: 0,
        throughputTps: 0,
        displayedPressure: 0,
        flowState: 'normal',
      },
    }
    const view = renderPipeline(rendezvous)
    const q1 = view.container.querySelector<SVGGElement>(
      '#queue-reader-to-throttler',
    )!

    expect(q1.style.getPropertyValue('--pipeline-queue-pressure-color'))
      .toBe('#ff6748')
    expect(view.container.querySelectorAll(
      '.pipeline-marker[data-marker-stage="queue1"][visibility="visible"]',
    )).toHaveLength(0)
  })

  it('keeps valve flow color owned by upstream queue1', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      queue1: {
        ...base.queue1,
        displayedPressure: 0,
        flowState: 'normal',
      },
      queue2: {
        ...base.queue2,
        displayedPressure: 1,
        flowState: 'backpressure',
      },
    }
    const view = renderPipeline(snapshot)
    const valve = view.container.querySelector<SVGGElement>('#throttler-actor')!
    const q2 = view.container.querySelector<SVGGElement>(
      '#queue-throttler-to-sender',
    )!

    expect(valve.style.getPropertyValue('--pipeline-valve-flow-color'))
      .toBe('#79d957')
    expect(q2.style.getPropertyValue('--pipeline-queue-pressure-color'))
      .toBe('#ff6748')
  })

  it('renders portrait viewBox, vertical ports, elbows, and shared marker paths', () => {
    const snapshot = activeSnapshot()
    const view = renderPipeline(snapshot, 'portrait')
    const svg = view.container.querySelector('svg')!
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
      senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
    })

    expect(svg.getAttribute('viewBox')).toBe(geometry.viewBox.value)
    expect(svg.dataset.layout).toBe('portrait')
    for (const queue of [snapshot.queue1, snapshot.queue2]) {
      const endpoints = geometry.queues[queue.id]
      const cable = view.container.querySelector<SVGPathElement>(
        `#queue-${queue.id} .pipeline-queue-cable`,
      )!
      expect(cable.getAttribute('d')?.startsWith(
        `M${endpoints.start.x} ${endpoints.start.y}`,
      )).toBe(true)
      expect(cable.getAttribute('d')?.endsWith(`V${endpoints.end.y}`))
        .toBe(true)
    }

    const throttler = view.container.querySelector('#throttler-actor')!
    expect(throttler.parentElement?.getAttribute('transform'))
      .toBe(`translate(${geometry.actors.throttler.transform.x} ${geometry.actors.throttler.transform.y})`)
    expect(throttler.querySelector('[data-connector-side="portrait-input"]'))
      .not.toBeNull()
    expect(throttler.querySelector('[data-connector-side="portrait-output"]'))
      .not.toBeNull()
    expect(throttler.querySelector('[data-connector-side="left"]')).toBeNull()
    expect(throttler.querySelector('[data-connector-side="right"]')).toBeNull()

    const sender = view.container.querySelector('#sender-actor')!
    expect(Number(sender.getAttribute('data-worker-columns')))
      .toBeLessThanOrEqual(8)
    expect(Number(sender.getAttribute('data-worker-rows'))).toBeGreaterThan(0)
  })

  it('keeps the offset bypass cradle inside portrait and right of centerline', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      throttler: {
        ...base.throttler,
        installationMode: {
          ...base.throttler.installationMode,
          applied: 'bypass',
          pending: null,
        },
      },
    }
    const view = renderPipeline(snapshot, 'portrait')
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
      senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
    })
    const target = view.container.querySelector<SVGRectElement>(
      '.pipeline-valve-installation-hit-area:not(.pipeline-valve-installation-hit-area--wheel-grip)',
    )!
    const left = Number(target.getAttribute('x')) +
      geometry.actors.throttler.transform.x
    const right = left + Number(target.getAttribute('width'))

    expect(left).toBeGreaterThan(240)
    expect(right).toBeLessThanOrEqual(480)
    expect(view.container.querySelector(
      '.pipeline-valve-detached-assembly--applied',
    )?.getAttribute('data-detached-anchor')).toBe('550 342')
  })

  it('follows the resolved layout class for queue handle cursors', () => {
    const snapshot = activeSnapshot()
    const cases = [
      {
        orientation: 'landscape',
        expectedCursor: 'ns-resize',
        expectedAriaOrientation: 'vertical',
      },
      {
        orientation: 'portrait',
        expectedCursor: 'ew-resize',
        expectedAriaOrientation: 'horizontal',
      },
    ] as const

    for (const testCase of cases) {
      const view = renderPipeline(snapshot, testCase.orientation)
      const svg = view.container.querySelector('svg')!
      const slider = view.container.querySelector<SVGGElement>(
        '.pipeline-queue-handle',
      )!

      expect(svg.classList).toContain(
        `pipeline-svg--${testCase.orientation}`,
      )
      expect(getComputedStyle(slider).cursor).toBe(testCase.expectedCursor)
      expect(slider.getAttribute('aria-orientation'))
        .toBe(testCase.expectedAriaOrientation)
      view.unmount()
    }

    const unavailableSnapshot: LoadgenSnapshot = {
      ...snapshot,
      queue1: {
        ...snapshot.queue1,
        capacity: {
          ...snapshot.queue1.capacity,
          applyMode: 'unavailable',
        },
      },
    }
    const unavailableView = renderPipeline(unavailableSnapshot, 'portrait')
    const disabledSlider = unavailableView.container.querySelector<SVGGElement>(
      '#queue-reader-to-throttler .pipeline-queue-handle',
    )!

    expect(disabledSlider.classList)
      .toContain('pipeline-queue-handle--disabled')
    expect(getComputedStyle(disabledSlider).cursor).toBe('default')
  })

  it('applies the portrait operational typography matrix', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      queue1: {
        ...base.queue1,
        blockedSenders: 3,
        oldestBlockedSenderMs: 1_250,
      },
    }
    const view = renderPipeline(snapshot, 'portrait')
    const assertTypography = (
      selector: string,
      fontSize: string,
      fontWeight: string,
    ) => {
      const element = view.container.querySelector<SVGTextElement>(selector)
      expect(element).not.toBeNull()
      const style = getComputedStyle(element!)
      expect(style.fontSize).toBe(fontSize)
      expect(style.fontWeight).toBe(fontWeight)
    }

    assertTypography('#reader-actor .pipeline-worker-primary', '15px', '650')
    assertTypography('#reader-actor .pipeline-worker-secondary', '12px', '600')
    assertTypography('#target-actor .pipeline-target-primary', '15px', '650')
    assertTypography('#target-actor .pipeline-target-secondary', '12px', '600')
    assertTypography('.pipeline-queue-metric.pipeline-small-strong', '15px', '700')
    assertTypography('.pipeline-queue-metric.pipeline-small', '13px', '600')
    assertTypography('.pipeline-queue-wait-status', '12px', '700')
    assertTypography('.pipeline-queue-capacity-status', '12px', '700')
    assertTypography('.pipeline-http-status', '14px', '700')
    assertTypography('.pipeline-http-throughput', '13px', '650')
    assertTypography('.pipeline-http-detail', '12px', '600')
    assertTypography('#requested-display', '14px', '650')
    assertTypography('.pipeline-valve-opening-label', '13px', '800')
    assertTypography('.pipeline-queue-handle__value', '11px', '750')
  })

  it.each(['landscape', 'portrait'] as const)(
    'renders deterministic Sender in-flight, backoff, and idle states in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        sender: {
          ...base.sender,
          workerStates: { idle: 1, inFlight: 1, backoff: 1 },
          workerSlots: [
            { id: 'sender-worker-0', ordinal: 0, state: 'backoff' },
            { id: 'sender-worker-1', ordinal: 1, state: 'idle' },
            { id: 'sender-worker-2', ordinal: 2, state: 'in-flight' },
          ],
          inFlightRequests: 1,
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const sender = view.container.querySelector('#sender-actor')!

      expect(sender.textContent).toContain('1 in-flight · 1 backoff')
      expect(sender.getAttribute('aria-label'))
        .toBe('Inspect sender, 1 idle, 1 in-flight, 1 backoff')
      expect(sender.querySelectorAll('.pipeline-worker--in-flight'))
        .toHaveLength(1)
      expect(sender.querySelectorAll('.pipeline-worker--backoff'))
        .toHaveLength(1)
      expect(sender.querySelectorAll('.pipeline-worker--idle')).toHaveLength(1)
      expect(
        [...sender.querySelectorAll('[data-worker-slot-id]')].map((slot) => ({
          id: slot.getAttribute('data-worker-slot-id'),
          ordinal: slot.getAttribute('data-worker-ordinal'),
          state: slot.getAttribute('data-worker-state'),
          className: slot.getAttribute('class'),
        })),
      ).toEqual([
        {
          id: 'sender-worker-0',
          ordinal: '0',
          state: 'backoff',
          className: 'pipeline-worker--backoff',
        },
        {
          id: 'sender-worker-1',
          ordinal: '1',
          state: 'idle',
          className: 'pipeline-worker--idle',
        },
        {
          id: 'sender-worker-2',
          ordinal: '2',
          state: 'in-flight',
          className: 'pipeline-worker--in-flight',
        },
      ])
      expect(
        getComputedStyle(
          sender.querySelector('.pipeline-worker--backoff .pipeline-worker-led')!,
        ).fill,
      ).toBe('var(--yellow)')
    },
  )

  it('does not synthesize per-slot states when backend slots are unknown', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      sender: {
        ...base.sender,
        workerStates: { idle: 1, inFlight: 1, backoff: 1 },
        workerSlots: null,
      },
    }
    const view = renderPipeline(snapshot)
    const sender = view.container.querySelector('#sender-actor')!

    expect(sender.querySelectorAll('[data-worker-slot-id]')).toHaveLength(0)
    expect(sender.querySelectorAll('.pipeline-worker--active')).toHaveLength(3)
    expect(sender.querySelectorAll('.pipeline-worker--backoff')).toHaveLength(0)
  })

  it('keeps a real backoff slot static amber with reduced motion', () => {
    const matchMedia = vi.mocked(window.matchMedia)
    const mediaQueryList = (query: string, matches: boolean) => ({
      matches,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })
    matchMedia.mockImplementation(
      (query) => ({
        ...mediaQueryList(
          query,
          query === '(prefers-reduced-motion: reduce)',
        ),
      }),
    )

    try {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        sender: {
          ...base.sender,
          workerStates: { idle: 2, inFlight: 0, backoff: 1 },
          workerSlots: [
            { id: 'sender-worker-0', ordinal: 0, state: 'idle' },
            { id: 'sender-worker-1', ordinal: 1, state: 'backoff' },
            { id: 'sender-worker-2', ordinal: 2, state: 'idle' },
          ],
        },
      }
      const view = renderPipeline(snapshot)
      const backoff = view.container.querySelector(
        '[data-worker-slot-id="sender-worker-1"]',
      )!

      expect(backoff.classList).toContain('pipeline-worker--backoff')
      expect(getComputedStyle(backoff).animationName).toBe('none')
      expect(getComputedStyle(backoff.querySelector('.pipeline-worker-led')!).fill)
        .toBe('var(--yellow)')
    } finally {
      matchMedia.mockImplementation((query) => mediaQueryList(query, false))
    }
  })

  it('shows timeout as a status without synthetic HTTP 504', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      http: {
        ...base.http,
        statusCode: null,
        lastOutcome: 'timeout',
        throughputTps: 1_000,
        inFlightRequests: 0,
      },
    }
    const view = renderPipeline(snapshot)
    const http = view.container.querySelector('#http-link')!

    expect(http.classList).toContain('pipeline-http--error')
    expect(http.querySelector('.pipeline-http-status')?.textContent)
      .toBe('TIMEOUT')
    expect(http.textContent).not.toContain('504')
  })

  it('keeps reduced-motion outcomes static without scale or glow', () => {
    const snapshot = activeSnapshot()
    const markers: MarkerLifecycleSnapshot = {
      revision: 1,
      reducedMotion: true,
      motionElapsedMs: 0,
      valveOpeningIndex: 11,
      markers: [{
        slotId: 'pipeline-marker-1',
        familyId: 'transaction-family-1',
        state: 'active',
        stage: 'target',
        phase: 1,
        queued: false,
        outcome: 'error',
        outcomeVisible: true,
        pulseProgress: 0.8,
      }],
    }
    const view = render(
      <svg><PipelineMarkers snapshot={snapshot} markers={markers} /></svg>,
    )
    const marker = view.container.querySelector<SVGCircleElement>(
      '.pipeline-marker--outcome',
    )!

    expect(marker.getAttribute('r')).toBe('4')
    expect(marker.style.filter).toBe('')
    expect(marker.style.opacity).toBe('')
    expect(marker.dataset.markerJitterX).toBe('0')
    expect(marker.dataset.markerJitterY).toBe('0')
  })

  it('hides and neutralizes an idle stale HTTP 503', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      http: {
        ...base.http,
        connectionState: 'connected',
        statusCode: 503,
        throughputTps: 0,
        inFlightRequests: 0,
      },
    }
    const view = renderPipeline(snapshot)
    const http = view.container.querySelector('#http-link')!

    expect(http.classList).toContain('pipeline-http--normal')
    expect(http.classList).not.toContain('pipeline-http--error')
    expect(http.querySelector('.pipeline-http-status')?.textContent)
      .toBe('HTTP —')
  })

  it('shows and classifies statuses whenever HTTP work is not known idle', () => {
    const base = activeSnapshot()
    const cases = [
      [503, 25_000, 0, 'pipeline-http--error'],
      [503, 0, 1, 'pipeline-http--error'],
      [404, 25_000, 0, 'pipeline-http--warning'],
      [503, null, null, 'pipeline-http--error'],
    ] as const

    for (const [statusCode, throughputTps, inFlightRequests, expectedClass]
      of cases) {
      const snapshot: LoadgenSnapshot = {
        ...base,
        http: {
          ...base.http,
          connectionState: 'connected',
          statusCode,
          throughputTps,
          inFlightRequests,
        },
      }
      const view = renderPipeline(snapshot)
      const http = view.container.querySelector('#http-link')!

      expect(http.classList).toContain(expectedClass)
      expect(http.querySelector('.pipeline-http-status')?.textContent)
        .toBe(`HTTP ${statusCode}`)
      view.unmount()
    }
  })

  it('keeps transport connection states authoritative', () => {
    const base = activeSnapshot()
    const cases = [
      ['error', 0, 0, 'pipeline-http--error', 'HTTP —'],
      ['disconnected', 25_000, 0, 'pipeline-http--stopped', 'HTTP 503'],
      ['connecting', 0, 1, 'pipeline-http--warning', 'HTTP 503'],
    ] as const

    for (const [
      connectionState,
      throughputTps,
      inFlightRequests,
      expectedClass,
      expectedStatus,
    ] of cases) {
      const snapshot: LoadgenSnapshot = {
        ...base,
        http: {
          ...base.http,
          connectionState,
          statusCode: 503,
          throughputTps,
          inFlightRequests,
        },
      }
      const view = renderPipeline(snapshot)
      const http = view.container.querySelector('#http-link')!

      expect(http.classList).toContain(expectedClass)
      expect(http.querySelector('.pipeline-http-status')?.textContent)
        .toBe(expectedStatus)
      if (connectionState !== 'error') {
        expect(http.classList).not.toContain('pipeline-http--error')
      }
      view.unmount()
    }
  })

  it('aligns landscape HTTP metrics and preserves portrait coordinates', () => {
    const snapshot = activeSnapshot()
    const landscape = renderPipeline(snapshot)
    const landscapeMetrics = [...landscape.container.querySelectorAll(
      '#http-link text',
    )]
    const queueTopRow = landscape.container.querySelector(
      '#queue-throttler-to-sender .pipeline-queue-metric',
    )!
    const senderTopRow = landscape.container.querySelector(
      '#sender-actor .pipeline-value',
    )!

    expect(landscapeMetrics.map((metric) => metric.getAttribute('y')))
      .toEqual(['507', '528', '549'])
    expect(landscapeMetrics[0].getAttribute('y'))
      .toBe(queueTopRow.getAttribute('y'))
    expect(Math.abs(
      Number(landscapeMetrics[0].getAttribute('y')) -
      Number(senderTopRow.getAttribute('y')),
    )).toBeLessThanOrEqual(3)
    landscape.unmount()

    const portrait = renderPipeline(snapshot, 'portrait')
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
      senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
    })
    const portraitMetrics = [...portrait.container.querySelectorAll(
      '#http-link text',
    )]
    expect(portraitMetrics.map((metric) => metric.getAttribute('x')))
      .toEqual(['340', '340', '340'])
    expect(portraitMetrics.map((metric) => metric.getAttribute('y')))
      .toEqual([
        String(geometry.http.metrics.statusY),
        String(geometry.http.metrics.throughputY),
        String(geometry.http.metrics.detailY),
      ])
  })
})
