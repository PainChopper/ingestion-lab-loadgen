/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { act, render } from '@testing-library/react'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  ChannelTelemetrySnapshot,
} from '../../model/loadgen'
import { ChannelFlowStateDeriver } from '../../model/channelFlowState'
import {
  createPipelineGeometry,
  PORTRAIT_THROTTLER_LIFT,
  CHANNEL_CABLE_ENDPOINTS,
  type TextPlacement,
} from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getFlowMarkerTarget,
  getChannelDepthFamilyTarget,
  MAX_PIPELINE_MARKERS,
} from './markerLifecycle'
import type { MarkerLifecycleSnapshot } from './markerLifecycle'
import { getChannelMarkerPathGeometry } from './markerPaths'
import { PipelineMarkers } from './PipelineMarkers'
import { PipelineSvg } from './PipelineSvg'
import { VALVE_FLANGES } from './throttlerValve'
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
      geometry.channels['reader-to-throttler'].end.x -
      geometry.channels['reader-to-throttler'].start.x,
    ).toBeCloseTo(205 + delta, 10)
    expect(
      geometry.channels['throttler-to-sender'].end.x -
      geometry.channels['throttler-to-sender'].start.x,
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
      const throttlerSlotInput = readerBottom + 247
      const throttlerInput = throttlerSlotInput - PORTRAIT_THROTTLER_LIFT
      const throttlerOutput = throttlerInput + 193
      const throttlerSlotOutput = throttlerSlotInput + 193
      const senderTop = throttlerSlotOutput + 222
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
      expect(geometry.channels['reader-to-throttler'].metrics.throughputY)
        .toBe(readerBottom + 60)
      expect(geometry.channels['throttler-to-sender'].metrics.throughputY)
        .toBe(throttlerSlotOutput + 42)
    },
  )

  it('owns landscape actor text placement and aligns primary metrics', () => {
    const geometry = createPipelineGeometry({
      orientation: 'landscape',
      landscapeContentWidth: 1440,
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const throttler = geometry.actors.throttler
    const throttlerBottom = throttler.bounds.y + throttler.bounds.height

    expect(geometry.actors.reader.metrics.primary).toMatchObject({
      y: 510,
      anchor: 'middle',
    })
    expect(geometry.actors.sender.metrics.primary).toMatchObject({
      y: 510,
      anchor: 'middle',
    })
    expect(throttler.metrics.installed.requested.caption).toEqual({
      x: 382,
      y: 489,
      anchor: 'middle',
    })
    expect(throttler.metrics.installed.admitted.value.y).toBe(510)
    expect(throttler.metrics.bypass.admitted.value).toEqual({
      x: 430,
      y: 510,
      anchor: 'middle',
    })
    expect(throttler.metrics.installed.requested.caption.y)
      .toBeGreaterThan(throttlerBottom)
    expect(throttler.metrics.bypass.admitted.caption.y)
      .toBeGreaterThan(throttlerBottom)
    expect(geometry.actors.target.labels.state.anchor).toBe('middle')
  })

  it.each(['installed', 'bypass'] as const)(
    'stacks portrait throttler %s metrics in the right telemetry rail',
    (installationMode) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: 7,
        senderWorkers: 32,
      })
      const throttler = geometry.actors.throttler
      if (!('portraitPipe' in throttler)) {
        throw new Error('portrait pipe geometry is missing')
      }
      const placements = installationMode === 'installed'
        ? [
          throttler.metrics.installed.requested.caption,
          throttler.metrics.installed.requested.value,
          throttler.metrics.installed.admitted.caption,
          throttler.metrics.installed.admitted.value,
        ]
        : [
          throttler.metrics.bypass.admitted.caption,
          throttler.metrics.bypass.admitted.value,
        ]
      const cardRight = throttler.bounds.x + throttler.bounds.width
      const cardBottom = throttler.bounds.y + throttler.bounds.height
      const inputChannelMetricBottom = Math.max(
        ...Object.values(geometry.channels['reader-to-throttler'].metrics)
          .filter((value): value is number => value !== 350),
      )
      const outputChannelMetricTop = Math.min(
        ...Object.values(geometry.channels['throttler-to-sender'].metrics)
          .filter((value): value is number => value !== 350),
      )
      const pipeRight = Math.max(
        throttler.portraitPipe.input.start.x,
        throttler.portraitPipe.output.start.x,
        ...throttler.portraitPipe.input.commands.flatMap((command) =>
          command.kind === 'quadratic'
            ? [command.control.x, command.end.x]
            : command.kind === 'horizontal'
              ? [command.x]
              : []
        ),
        ...throttler.portraitPipe.output.commands.flatMap((command) =>
          command.kind === 'quadratic'
            ? [command.control.x, command.end.x]
            : command.kind === 'horizontal'
              ? [command.x]
              : []
        ),
      ) + throttler.transform.x

      for (const placement of placements) {
        const globalX = placement.x + throttler.transform.x
        const globalY = placement.y + throttler.transform.y

        expect(globalX).toBeGreaterThan(cardRight)
        expect(globalX).toBeGreaterThan(pipeRight)
        expect(globalX).toBeGreaterThan(throttler.ports.output.x)
        expect(globalY).toBeGreaterThan(inputChannelMetricBottom)
        expect(globalY).toBeLessThan(outputChannelMetricTop)
        expect(globalY).toBeLessThan(cardBottom)
        expect(globalY).toBeLessThan(geometry.actors.sender.title.y)
        expect(placement.anchor).toBe('start')
      }

      const globalPlacements = placements.map((placement) => ({
        x: placement.x + throttler.transform.x,
        y: placement.y + throttler.transform.y,
      }))
      if (installationMode === 'installed') {
        expect(globalPlacements).toEqual([
          { x: 330, y: 500 },
          { x: 330, y: 521 },
          { x: 330, y: 560 },
          { x: 330, y: 581 },
        ])
        expect(globalPlacements[1].y).toBeLessThan(globalPlacements[2].y)
      } else {
        expect(globalPlacements).toEqual([
          { x: 330, y: 560 },
          { x: 330, y: 581 },
        ])
        expect(throttler.metrics.bypass.admitted)
          .toEqual(throttler.metrics.installed.admitted)
      }

      expect(globalPlacements.some(({ y }) => y > cardBottom)).toBe(false)
    },
  )

  it('owns monotonic portrait pipe routes with channel-style rounded bends', () => {
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const throttler = geometry.actors.throttler
    if (!('portraitPipe' in throttler)) {
      throw new Error('portrait pipe geometry is missing')
    }
    const { input, output } = throttler.portraitPipe
    const globalPoint = (point: { x: number; y: number }) => ({
      x: point.x + throttler.transform.x,
      y: point.y + throttler.transform.y,
    })
    const expectRoundedMonotonicRoute = (path: typeof input) => {
      let current = path.start
      const horizontalDirections: number[] = []
      let curveCount = 0

      for (const command of path.commands) {
        const next = command.kind === 'horizontal'
          ? { x: command.x, y: current.y }
          : command.kind === 'vertical'
            ? { x: current.x, y: command.y }
            : command.end

        expect(next.y).toBeGreaterThanOrEqual(current.y)
        if (command.kind === 'quadratic') {
          curveCount += 1
          expect(command.control.y).toBeGreaterThanOrEqual(current.y)
          expect(command.end.y).toBeGreaterThanOrEqual(command.control.y)
          expect(Math.hypot(
            command.control.x - current.x,
            command.control.y - current.y,
          )).toBe(16)
          expect(Math.hypot(
            command.end.x - command.control.x,
            command.end.y - command.control.y,
          )).toBe(16)
        }
        const horizontalDirection = Math.sign(next.x - current.x)
        if (horizontalDirection !== 0) {
          horizontalDirections.push(horizontalDirection)
        }
        current = next
      }

      const directionChanges = horizontalDirections.slice(1).filter(
        (direction, index) => direction !== horizontalDirections[index],
      )
      expect(current).toEqual(path.end)
      expect(curveCount).toBe(3)
      expect(directionChanges.length).toBeLessThanOrEqual(1)
    }

    expect(globalPoint(input.start)).toEqual(throttler.ports.input)
    expect(input.end).toEqual({ x: VALVE_FLANGES.left, y: 415 })
    expect(output.start).toEqual({ x: VALVE_FLANGES.right, y: 415 })
    expect(globalPoint(output.end)).toEqual(throttler.ports.output)
    expect(input.d).toBe(
      'M430 282 V306 Q430 322 414 322 H390 Q374 322 374 338 V399 Q374 415 390 415 H401',
    )
    expect(output.d).toBe(
      'M459 415 H470 Q486 415 486 431 V443 Q486 459 470 459 H446 Q430 459 430 475',
    )
    expect(input.commands[0]).toEqual({ kind: 'vertical', y: 306 })
    expect(input.commands.at(-1)).toEqual({ kind: 'horizontal', x: 401 })
    expect(output.commands[0]).toEqual({ kind: 'horizontal', x: 470 })
    expect(output.commands.at(-1)).toEqual({
      kind: 'quadratic',
      control: { x: output.end.x, y: 459 },
      end: output.end,
    })
    expectRoundedMonotonicRoute(input)
    expectRoundedMonotonicRoute(output)
  })
})

afterAll(() => {
  styleElement.remove()
})

function activeChannel<TChannel extends ChannelTelemetrySnapshot>(
  channel: TChannel,
  applied: number,
  candidate: number | null,
): TChannel {
  return {
    ...channel,
    depthBatches: Math.min(applied, 4),
    throughputTps: 50_000,
    inputTps: 50_000,
    outputTps: 50_000,
    inputTransactionsPerSecond: 50_000,
    outputTransactionsPerSecond: 50_000,
    inputBatchesPerSecond: 10,
    outputBatchesPerSecond: 10,
    sentBatchesTotal: 20,
    receivedBatchesTotal: 18,
    capacity: {
      ...channel.capacity,
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
    readerChannel: activeChannel(base.readerChannel, 10, 0),
    senderChannel: activeChannel(base.senderChannel, 160, 50),
  }
  return new ChannelFlowStateDeriver().derive(telemetry, 0)
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
      onChannelCapacityChange={vi.fn()}
      requestedTpsPreview={null}
      onRequestedTpsPreviewChange={vi.fn()}
      onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
      onInstallationModeChange={vi.fn().mockResolvedValue(true)}
      orientation={orientation}
      geometry={geometry}
    />,
  )
}

function expectTextPlacement(
  element: Element | null,
  placement: TextPlacement,
) {
  expect(element).not.toBeNull()
  expect([
    element?.getAttribute('x'),
    element?.getAttribute('y'),
    element?.getAttribute('text-anchor'),
  ]).toEqual([
    String(placement.x),
    String(placement.y),
    placement.anchor,
  ])
}

function withWorkerCount(
  snapshot: LoadgenSnapshot,
  count: number,
): LoadgenSnapshot {
  const control = (workers: LoadgenSnapshot['reader']['workers']) => ({
    ...workers,
    applied: count,
    preview: null,
    pending: null,
    min: 0,
    max: 32,
  })

  return {
    ...snapshot,
    reader: {
      ...snapshot.reader,
      workers: control(snapshot.reader.workers),
    },
    sender: {
      ...snapshot.sender,
      workers: control(snapshot.sender.workers),
    },
  }
}

function pipelineElement(snapshot: LoadgenSnapshot) {
  return (
    <PipelineSvg
      snapshot={snapshot}
      selectedId={null}
      onSelect={vi.fn()}
      onWorkerCountChange={vi.fn()}
      onChannelCapacityChange={vi.fn()}
      requestedTpsPreview={null}
      onRequestedTpsPreviewChange={vi.fn()}
      onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
      onInstallationModeChange={vi.fn().mockResolvedValue(true)}
    />
  )
}

describe('PipelineSvg marker wiring', () => {
  it('keeps Simulation Reader activity lifecycle-driven while HTTP uses read rate', () => {
    const adapter = new SimulationAdapter()
    const base = adapter.getSnapshot()
    adapter.dispose()
    const withReadRate = (
      adapterKind: 'simulation' | 'http',
      runState: 'idle' | 'running' | 'paused',
      readTps: number,
    ) =>
      new ChannelFlowStateDeriver().derive({
        ...base,
        adapterKind,
        runState,
        reader: { ...base.reader, state: runState, readTps },
        throttler: { ...base.throttler, state: runState },
        sender: { ...base.sender, state: runState },
      }, 0)

    const simulation = renderPipeline(withReadRate('simulation', 'running', 0))
    expect(simulation.container.querySelectorAll('#reader-actor .pipeline-worker--active'))
      .toHaveLength(base.reader.workers.applied ?? 0)
    simulation.unmount()

    const idle = renderPipeline(withReadRate('http', 'idle', 0))
    expect(idle.container.querySelectorAll('#reader-actor .pipeline-worker--active'))
      .toHaveLength(0)
    idle.unmount()

    const running = renderPipeline(withReadRate('http', 'running', 1))
    expect(running.container.querySelectorAll('#reader-actor .pipeline-worker--active'))
      .toHaveLength(base.reader.workers.applied ?? 0)
    running.unmount()

    const paused = renderPipeline(withReadRate('http', 'paused', 1))
    expect(paused.container.querySelectorAll('#reader-actor .pipeline-worker--active'))
      .toHaveLength(base.reader.workers.applied ?? 0)
    paused.unmount()
  })

  it('keeps landscape actor bottoms, ports, and link endpoints stable across worker boundaries', () => {
    const cases = [
      { count: 0, reader: { top: 355, height: 120 }, sender: { top: 355, height: 120 } },
      { count: 2, reader: { top: 355, height: 120 }, sender: { top: 355, height: 120 } },
      { count: 3, reader: { top: 326, height: 149 }, sender: { top: 326, height: 149 } },
      { count: 7, reader: { top: 170, height: 305 }, sender: { top: 170, height: 305 } },
      { count: 8, reader: { top: 131, height: 344 }, sender: { top: 170, height: 305 } },
      { count: 16, reader: { top: -181, height: 656 }, sender: { top: 170, height: 305 } },
      { count: 32, reader: { top: -805, height: 1280 }, sender: { top: 170, height: 305 } },
    ] as const
    const base = activeSnapshot()
    let snapshot = withWorkerCount(base, cases[0].count)
    const view = render(pipelineElement(snapshot))
    let stableEndpointInventory: unknown = null

    for (const testCase of cases) {
      snapshot = withWorkerCount(base, testCase.count)
      view.rerender(pipelineElement(snapshot))
      const geometry = createPipelineGeometry({
        orientation: 'landscape',
        readerWorkers: testCase.count,
        senderWorkers: testCase.count,
      })
      const actorCases = [
        {
          actor: 'reader' as const,
          expected: testCase.reader,
          ports: [geometry.actors.reader.ports.output],
          mode: 'detailed',
          columns: 1,
          rows: testCase.count,
        },
        {
          actor: 'sender' as const,
          expected: testCase.sender,
          ports: [
            geometry.actors.sender.ports.input,
            geometry.actors.sender.ports.output,
          ],
          mode: testCase.count > 7 ? 'compact' : 'detailed',
          columns: testCase.count > 7 ? 4 : 1,
          rows: testCase.count > 7
            ? Math.ceil(testCase.count / 4)
            : testCase.count,
        },
      ]

      for (const actorCase of actorCases) {
        const actor = view.container.querySelector<SVGGElement>(
          `#${actorCase.actor}-actor`,
        )!
        const box = actor.querySelector<SVGRectElement>(
          '.pipeline-actor-box',
        )!
        const ports = [...actor.querySelectorAll<SVGCircleElement>(
          '.pipeline-port',
        )].map((port) => ({
          x: Number(port.getAttribute('cx')),
          y: Number(port.getAttribute('cy')),
        }))

        expect({
          top: Number(box.getAttribute('y')),
          height: Number(box.getAttribute('height')),
        }).toEqual(actorCase.expected)
        expect(Number(box.getAttribute('y')) +
          Number(box.getAttribute('height'))).toBe(475)
        expect(actor.dataset.workerCount).toBe(String(testCase.count))
        expect(actor.dataset.workerLayout).toBe(actorCase.mode)
        expect(actor.dataset.workerColumns).toBe(String(actorCase.columns))
        expect(actor.dataset.workerRows).toBe(String(actorCase.rows))
        expect(ports).toEqual(actorCase.ports)
      }

      const channelPaths = Object.values(geometry.channels).map((channel) => {
        const group = view.container.querySelector(
          `#channel-${
            channel === geometry.channels['reader-to-throttler']
              ? 'reader-to-throttler'
              : 'throttler-to-sender'
          }`,
        )!
        const paths = [...group.querySelectorAll<SVGPathElement>(
          '.pipeline-channel-cable, .pipeline-channel-requested-cable',
        )]

        expect(paths.length).toBeGreaterThan(0)
        for (const path of paths) {
          expect(path.getAttribute('d'))
            .toMatch(new RegExp(`^M${channel.start.x} ${channel.start.y}\\b`))
          expect(path.getAttribute('d'))
            .toMatch(new RegExp(`H${channel.end.x}$`))
        }

        return paths.map((path) => path.getAttribute('d'))
      })
      const httpPath = view.container.querySelector<SVGPathElement>(
        '#http-link .pipeline-http-line',
      )!
      expect(httpPath.getAttribute('d')).toBe(
        `M${geometry.http.start.x} ${geometry.http.start.y} H${geometry.http.end.x}`,
      )

      const endpointInventory = {
        readerPorts: actorCases[0].ports,
        senderPorts: actorCases[1].ports,
        channelPaths,
        httpPath: httpPath.getAttribute('d'),
      }
      if (stableEndpointInventory === null) {
        stableEndpointInventory = endpointInventory
      } else {
        expect(endpointInventory).toEqual(stableEndpointInventory)
      }

      const svgChildren = [...view.container.querySelector('svg')!.children]
      expect(svgChildren.indexOf(
        view.container.querySelector('#http-link')!,
      )).toBeLessThan(svgChildren.indexOf(
        view.container.querySelector('#reader-actor')!,
      ))
      expect(svgChildren.indexOf(
        view.container.querySelector('#channel-throttler-to-sender')!,
      )).toBeLessThan(svgChildren.indexOf(
        view.container.querySelector('#sender-actor')!,
      ))
    }
  })

  it.each([
    { orientation: 'landscape', installationMode: 'installed' },
    { orientation: 'landscape', installationMode: 'bypass' },
    { orientation: 'portrait', installationMode: 'installed' },
    { orientation: 'portrait', installationMode: 'bypass' },
  ] as const)(
    'renders geometry-owned actor text in $orientation $installationMode',
    ({ orientation, installationMode }) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        reader: {
          ...base.reader,
          limitationReason: 'downstream-backpressure',
        },
        throttler: {
          ...base.throttler,
          installationMode: {
            ...base.throttler.installationMode,
            applied: installationMode,
            pending: null,
          },
        },
      }
      const geometry = createPipelineGeometry({
        orientation,
        readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
        senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
      })
      const view = renderPipeline(snapshot, orientation)
      const actorGeometry = geometry.actors

      expectTextPlacement(
        view.container.querySelector('.pipeline-title--reader'),
        actorGeometry.reader.title,
      )
      expectTextPlacement(
        view.container.querySelector('#reader-actor .pipeline-worker-primary'),
        actorGeometry.reader.metrics.primary,
      )
      expectTextPlacement(
        view.container.querySelector('#reader-actor .pipeline-worker-secondary'),
        actorGeometry.reader.metrics.secondary,
      )
      expectTextPlacement(
        view.container.querySelector('#reader-actor .pipeline-worker-status'),
        actorGeometry.reader.metrics.status,
      )
      expectTextPlacement(
        view.container.querySelector('.pipeline-title--sender'),
        actorGeometry.sender.title,
      )
      expectTextPlacement(
        view.container.querySelector('#sender-actor .pipeline-worker-primary'),
        actorGeometry.sender.metrics.primary,
      )
      expectTextPlacement(
        view.container.querySelector('#sender-actor .pipeline-worker-secondary'),
        actorGeometry.sender.metrics.secondary,
      )
      expectTextPlacement(
        view.container.querySelector('.pipeline-title--throttler'),
        actorGeometry.throttler.renderTitle,
      )
      expectTextPlacement(
        view.container.querySelector('.pipeline-title--target'),
        actorGeometry.target.title,
      )
      expectTextPlacement(
        view.container.querySelector('#target-actor .pipeline-target-secondary'),
        actorGeometry.target.labels.caption,
      )
      expectTextPlacement(
        view.container.querySelector('#target-actor .pipeline-target-primary'),
        actorGeometry.target.labels.value,
      )
      expectTextPlacement(
        view.container.querySelector('#target-actor .pipeline-target-failure'),
        actorGeometry.target.labels.failure,
      )
      expectTextPlacement(
        view.container.querySelectorAll(
          '#target-actor .pipeline-target-secondary',
        ).item(2),
        actorGeometry.target.labels.state,
      )

      const throttlerMetrics = installationMode === 'installed'
        ? actorGeometry.throttler.metrics.installed
        : actorGeometry.throttler.metrics.bypass
      const requestedCaption = view.container.querySelector(
        '[data-actor-metric="requested-caption"]',
      )
      const requestedValue = view.container.querySelector(
        '[data-actor-metric="requested-value"]',
      )
      if (installationMode === 'installed') {
        expectTextPlacement(
          requestedCaption,
          actorGeometry.throttler.metrics.installed.requested.caption,
        )
        expectTextPlacement(
          requestedValue,
          actorGeometry.throttler.metrics.installed.requested.value,
        )
      } else {
        expect(requestedCaption).toBeNull()
        expect(requestedValue).toBeNull()
      }
      const admittedCaption = view.container.querySelector(
        '[data-actor-metric="admitted-caption"]',
      )
      const admittedValue = view.container.querySelector(
        '[data-actor-metric="admitted-value"]',
      )
      expectTextPlacement(admittedCaption, throttlerMetrics.admitted.caption)
      expectTextPlacement(admittedValue, throttlerMetrics.admitted.value)

      if (orientation === 'portrait') {
        const throttler = actorGeometry.throttler
        const throttlerRight = throttler.bounds.x + throttler.bounds.width
        const throttlerBottom = throttler.bounds.y + throttler.bounds.height
        const renderedMetrics = installationMode === 'installed'
          ? [requestedCaption, requestedValue, admittedCaption, admittedValue]
          : [admittedCaption, admittedValue]
        const globalPlacements = renderedMetrics.map(
          (element) => ({
            x: Number(element?.getAttribute('x')) + throttler.transform.x,
            y: Number(element?.getAttribute('y')) + throttler.transform.y,
            anchor: element?.getAttribute('text-anchor'),
          }),
        )

        for (const placement of globalPlacements) {
          expect(placement.x).toBeGreaterThan(throttlerRight)
          expect(placement.x).toBeGreaterThan(throttler.ports.output.x)
          expect(Math.min(
            ...Object.values(
              geometry.channels['throttler-to-sender'].metrics,
            ).filter((value): value is number => value !== 350)
              .map((channelY) => Math.abs(placement.y - channelY)),
          )).toBeGreaterThanOrEqual(24)
          expect(placement.y).toBeLessThan(actorGeometry.sender.title.y)
          expect(placement.y).toBeLessThan(throttlerBottom)
          expect(placement.anchor).toBe('start')
        }
        expect(globalPlacements.some(({ y }) => y > throttlerBottom))
          .toBe(false)
      }
    },
  )

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

  it('projects the unified marker pool onto applied channel paths', () => {
    const snapshot = activeSnapshot()
    const telemetry = markerTelemetryFromSnapshot(snapshot, false)
    const { container } = renderPipeline(snapshot)
    const cases = [
      { channel: snapshot.readerChannel, stage: 'readerChannel' as const },
      { channel: snapshot.senderChannel, stage: 'senderChannel' as const },
    ] as const

    for (const testCase of cases) {
      const channelGroup = container.querySelector(`#channel-${testCase.channel.id}`)!
      const cable = channelGroup.querySelector<SVGPathElement>('.pipeline-channel-cable')!
      const markers = [...container.querySelectorAll<SVGCircleElement>(
        `.pipeline-marker[data-marker-stage="${testCase.stage}"]`,
      )]
      const path = cable.getAttribute('d')!
      const geometry = getChannelMarkerPathGeometry(
        testCase.channel.id,
        testCase.channel.capacity,
      )
      const endpoints = CHANNEL_CABLE_ENDPOINTS[testCase.channel.id]

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

  it('keeps marker ownership outside ChannelCable and removes request UI after apply', () => {
    const pending = activeSnapshot()
    const view = renderPipeline(pending)

    for (const channel of [pending.readerChannel, pending.senderChannel]) {
      const channelGroup = view.container.querySelector(`#channel-${channel.id}`)!
      const cable = channelGroup.querySelector('.pipeline-channel-cable')!
      const appliedLabel = channelGroup.querySelector('.pipeline-channel-capacity-applied')!
      const metrics = [...channelGroup.querySelectorAll('.pipeline-channel-metric')]
      const slider = channelGroup.querySelector('.pipeline-channel-handle')!
      const children = [...channelGroup.children]

      expect(channelGroup.querySelector('.pipeline-channel-requested-cable')).toBeNull()
      expect(channelGroup.querySelector('.pipeline-marker')).toBeNull()
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
      readerChannel: activeChannel(pending.readerChannel, 0, null),
      senderChannel: activeChannel(pending.senderChannel, 50, null),
    }
    view.rerender(
      <PipelineSvg
        snapshot={applied}
        selectedId={null}
        onSelect={vi.fn()}
        onWorkerCountChange={vi.fn()}
        onChannelCapacityChange={vi.fn()}
        requestedTpsPreview={null}
        onRequestedTpsPreviewChange={vi.fn()}
        onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
        onInstallationModeChange={vi.fn().mockResolvedValue(true)}
      />,
    )

    for (const channel of [applied.readerChannel, applied.senderChannel]) {
      const channelGroup = view.container.querySelector(`#channel-${channel.id}`)!
      expect(channelGroup.querySelector('.pipeline-channel-requested-cable')).toBeNull()
      expect(channelGroup.querySelector('.pipeline-channel-capacity-applied')).toBeNull()
      expect(channelGroup.querySelector('.pipeline-channel-capacity-status')).toBeNull()
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
        buffered: false,
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
      readerChannel: { ...base.readerChannel, throughputTps: 250_000 },
      senderChannel: { ...base.senderChannel, throughputTps: 250_000 },
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
      readerChannel: { ...moving.readerChannel, throughputTps: 0 },
      senderChannel: { ...moving.senderChannel, throughputTps: 0 },
    }
    view.rerender(
      <PipelineSvg
        snapshot={acceptedZero}
        selectedId={null}
        onSelect={vi.fn()}
        onWorkerCountChange={vi.fn()}
        onChannelCapacityChange={vi.fn()}
        requestedTpsPreview={null}
        onRequestedTpsPreviewChange={vi.fn()}
        onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
        onInstallationModeChange={vi.fn().mockResolvedValue(true)}
      />,
    )
    const atZero = readVisibleFamilies(view.container)

    expect(acceptedZero.throttler.requestedTps.applied).toBe(0)
    expect(acceptedZero.readerChannel.throughputTps).toBe(0)
    expect(acceptedZero.senderChannel.throughputTps).toBe(0)
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
        onChannelCapacityChange={vi.fn()}
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
      readerChannel: { ...snapshot.readerChannel, throughputTps },
      senderChannel: { ...snapshot.senderChannel, throughputTps },
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
          onChannelCapacityChange={vi.fn()}
          requestedTpsPreview={null}
          onRequestedTpsPreviewChange={vi.fn()}
          onRequestedTpsChange={vi.fn().mockResolvedValue(true)}
          onInstallationModeChange={vi.fn().mockResolvedValue(true)}
        />,
      )
      const after = readActiveFlow(view.container)
      const survivors = [...after].filter(([familyId]) => before.has(familyId))

      expect(after.size).toBe(
        getChannelDepthFamilyTarget(
          snapshot.readerChannel.depthBatches,
          snapshot.readerChannel.capacity.applied ?? 0,
        ) +
          getChannelDepthFamilyTarget(
            snapshot.senderChannel.depthBatches,
            snapshot.senderChannel.capacity.applied ?? 0,
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
      readerChannel: {
        ...base.readerChannel,
        depthBatches: 12,
        capacity: {
          ...base.readerChannel.capacity,
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
      buffered: true,
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
            readerChannel: { ...withoutCandidate.readerChannel, depthBatches: 3 },
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

  it('keeps one uniform vacuum and an empty green senderChannel projection at drain end', () => {
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
      readerChannel: {
        ...base.readerChannel,
        depthBatches: 0,
        throughputTps: 0,
        flowState: 'normal',
        displayedPressure: 0,
      },
      senderChannel: {
        ...base.senderChannel,
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
      '.pipeline-marker[data-marker-stage="senderChannel"][visibility="visible"]',
    )).toHaveLength(0)
    const senderChannel = view.container.querySelector<SVGGElement>(
      '#channel-throttler-to-sender',
    )!
    expect(senderChannel.style.getPropertyValue('--pipeline-channel-pressure-color'))
      .toBe('#79d957')
  })

  it('projects rendezvous pressure without occupancy markers', () => {
    const base = activeSnapshot()
    const rendezvous: LoadgenSnapshot = {
      ...base,
      readerChannel: {
        ...base.readerChannel,
        depthBatches: 0,
        throughputTps: 0,
        blockedSenders: 1,
        oldestBlockedSenderMs: 10,
        displayedPressure: 1,
        flowState: 'backpressure',
        capacity: {
          ...base.readerChannel.capacity,
          applied: 0,
          preview: 12,
          pending: 12,
        },
      },
      senderChannel: {
        ...base.senderChannel,
        depthBatches: 0,
        throughputTps: 0,
        displayedPressure: 0,
        flowState: 'normal',
      },
    }
    const view = renderPipeline(rendezvous)
    const readerChannel = view.container.querySelector<SVGGElement>(
      '#channel-reader-to-throttler',
    )!

    expect(readerChannel.style.getPropertyValue('--pipeline-channel-pressure-color'))
      .toBe('#ff6748')
    expect(view.container.querySelectorAll(
      '.pipeline-marker[data-marker-stage="readerChannel"][visibility="visible"]',
    )).toHaveLength(0)
  })

  it('keeps valve flow color owned by upstream readerChannel', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      readerChannel: {
        ...base.readerChannel,
        displayedPressure: 0,
        flowState: 'normal',
      },
      senderChannel: {
        ...base.senderChannel,
        displayedPressure: 1,
        flowState: 'backpressure',
      },
    }
    const view = renderPipeline(snapshot)
    const valve = view.container.querySelector<SVGGElement>('#throttler-actor')!
    const senderChannel = view.container.querySelector<SVGGElement>(
      '#channel-throttler-to-sender',
    )!

    expect(valve.style.getPropertyValue('--pipeline-valve-flow-color'))
      .toBe('#79d957')
    expect(senderChannel.style.getPropertyValue('--pipeline-channel-pressure-color'))
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
    for (const channel of [snapshot.readerChannel, snapshot.senderChannel]) {
      const endpoints = geometry.channels[channel.id]
      const cable = view.container.querySelector<SVGPathElement>(
        `#channel-${channel.id} .pipeline-channel-cable`,
      )!
      expect(cable.getAttribute('d')?.startsWith(
        `M${endpoints.start.x} ${endpoints.start.y}`,
      )).toBe(true)
      expect(cable.getAttribute('d')?.endsWith(`V${endpoints.end.y}`))
        .toBe(true)
    }

    const throttler = view.container.querySelector('#throttler-actor')!
    const throttlerGeometry = geometry.actors.throttler
    if (!('portraitPipe' in throttlerGeometry)) {
      throw new Error('portrait pipe geometry is missing')
    }
    expect(throttler.parentElement?.getAttribute('transform'))
      .toBe(`translate(${throttlerGeometry.transform.x} ${throttlerGeometry.transform.y})`)
    const portraitInput = throttler.querySelector<SVGPathElement>(
      '[data-connector-side="portrait-input"]',
    )!
    const portraitOutput = throttler.querySelector<SVGPathElement>(
      '[data-connector-side="portrait-output"]',
    )!
    expect(portraitInput.getAttribute('d'))
      .toBe(throttlerGeometry.portraitPipe.input.d)
    expect(portraitOutput.getAttribute('d'))
      .toBe(throttlerGeometry.portraitPipe.output.d)
    expect(portraitInput.getAttribute('d')?.match(/Q/g)).toHaveLength(3)
    expect(portraitOutput.getAttribute('d')?.match(/Q/g)).toHaveLength(3)
    expect(portraitInput.getAttribute('d')).not.toMatch(/[CAS]/)
    expect(portraitOutput.getAttribute('d')).not.toMatch(/[CAS]/)
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

  it('follows the resolved layout class for channel handle cursors', () => {
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
        '.pipeline-channel-handle',
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
      readerChannel: {
        ...snapshot.readerChannel,
        capacity: {
          ...snapshot.readerChannel.capacity,
          applyMode: 'unavailable',
        },
      },
    }
    const unavailableView = renderPipeline(unavailableSnapshot, 'portrait')
    const disabledSlider = unavailableView.container.querySelector<SVGGElement>(
      '#channel-reader-to-throttler .pipeline-channel-handle',
    )!

    expect(disabledSlider.classList)
      .toContain('pipeline-channel-handle--disabled')
    expect(getComputedStyle(disabledSlider).cursor).toBe('default')
  })

  it('applies the portrait operational typography matrix', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      readerChannel: {
        ...base.readerChannel,
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
    assertTypography('.pipeline-channel-metric.pipeline-small-strong', '15px', '700')
    assertTypography('.pipeline-channel-metric.pipeline-small', '13px', '600')
    assertTypography('.pipeline-channel-wait-status', '12px', '700')
    assertTypography('.pipeline-channel-capacity-status', '12px', '700')
    assertTypography('.pipeline-http-status', '14px', '700')
    assertTypography('.pipeline-http-throughput', '13px', '650')
    assertTypography('.pipeline-http-detail', '12px', '600')
    assertTypography('#requested-display', '14px', '650')
    assertTypography('.pipeline-valve-opening-label', '13px', '800')
    assertTypography('.pipeline-channel-handle__value', '11px', '750')
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
        buffered: false,
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
    const channelTopRow = landscape.container.querySelector(
      '#channel-throttler-to-sender .pipeline-channel-metric',
    )!
    const senderTopRow = landscape.container.querySelector(
      '#sender-actor .pipeline-value',
    )!

    expect(landscapeMetrics.map((metric) => metric.getAttribute('y')))
      .toEqual(['507', '528', '549'])
    expect(landscapeMetrics[0].getAttribute('y'))
      .toBe(channelTopRow.getAttribute('y'))
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
