/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { render } from '@testing-library/react'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  ChannelTelemetrySnapshot,
} from '../../model/loadgen'
import {
  CHANNEL_PRESSURE_GREEN,
  CHANNEL_PRESSURE_RED,
  CHANNEL_PRESSURE_YELLOW,
  ChannelFlowStateDeriver,
} from '../../model/channelFlowState'
import { createPipelineGeometry, FLOW_BASELINE } from './geometry'
import { LANDSCAPE_CHANNEL_CABLE_HEIGHT } from './channelCableGeometry'
import type { PipelineOrientation } from './pipelineLayout'
import { PipelineSvg } from './PipelineSvg'
import { VALVE_FLANGES } from './throttlerValve'
import { normalizedWorkerCount } from './workerActorLayout'
import type { DesiredControl, LiveControls } from '../../hooks/useDesiredControl'

const styleElement = document.createElement('style')
const pipelineStyles = readFileSync(
  resolve(process.cwd(), 'src/components/pipeline/PipelineSvg.css'),
  'utf8',
)

beforeAll(() => {
  styleElement.textContent = pipelineStyles
  document.head.append(styleElement)
})

it('keeps channel pressure colors aligned with the stylesheet palette', () => {
  const stylesheet = document.createElement('style')
  stylesheet.textContent = readFileSync(resolve(process.cwd(), 'src/index.css'), 'utf8')
  document.head.append(stylesheet)
  try {
    const palette = getComputedStyle(document.documentElement)
    expect(palette.getPropertyValue('--green').trim()).toBe(CHANNEL_PRESSURE_GREEN)
    expect(palette.getPropertyValue('--yellow').trim()).toBe(CHANNEL_PRESSURE_YELLOW)
    expect(palette.getPropertyValue('--red').trim()).toBe(CHANNEL_PRESSURE_RED)
  } finally {
    stylesheet.remove()
  }
})

describe('responsive pipeline geometry', () => {
  it.each(['landscape', 'portrait'] as const)(
    'connects ports and contains Target stages in %s',
    (orientation) => {
      const geometry = createPipelineGeometry({ orientation, readerWorkers: 7, senderWorkers: 32 })
      const { reader, throttler, sender, target } = geometry.actors
      expect(geometry.channels['reader-to-throttler'].start).toEqual(reader.ports.output)
      expect(geometry.channels['reader-to-throttler'].end).toEqual(throttler.ports.input)
      expect(geometry.channels['throttler-to-sender'].start).toEqual(throttler.ports.output)
      expect(geometry.channels['throttler-to-sender'].end).toEqual(sender.ports.input)
      expect(geometry.http.start).toEqual(sender.ports.output)
      expect(geometry.http.end).toEqual(target.ports.input)
      for (const stage of target.stages) {
        expect(stage.y).toBeGreaterThanOrEqual(target.bounds.y)
        expect(stage.y + stage.height).toBeLessThanOrEqual(target.bounds.y + target.bounds.height)
      }
    },
  )

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
      height: 582,
      value: `0 22 ${width} 582`,
    })
    expect(geometry.stationDelta).toBe(delta)
    expect(geometry.batchLabel.anchor.x).toBeCloseTo(430 + delta, 10)
    expect(geometry.batchLabel.anchor.y).toBe(112)
    expect(geometry.batchLabel.guard.x).toBeCloseTo(334 + delta, 10)
    expect(geometry.batchLabel.guard).toMatchObject({
      y: 84,
      width: 192,
      height: 56,
    })
    expect(geometry.batchLabel.guard.y + geometry.batchLabel.guard.height).toBeLessThan(
      FLOW_BASELINE - LANDSCAPE_CHANNEL_CABLE_HEIGHT,
    )
    expect(geometry.actors.reader.bounds.width).toBe(120)
    expect(geometry.actors.throttler.bounds.width).toBe(150)
    expect(geometry.actors.sender.bounds.width).toBe(120)
    expect(geometry.actors.target.bounds.width).toBe(190)
    expect(width - (geometry.actors.target.bounds.x + geometry.actors.target.bounds.width)).toBe(30)
    expect(
      geometry.channels['reader-to-throttler'].end.x -
        geometry.channels['reader-to-throttler'].start.x,
    ).toBeCloseTo(205 + delta, 10)
    expect(
      geometry.channels['throttler-to-sender'].end.x -
        geometry.channels['throttler-to-sender'].start.x,
    ).toBeCloseTo(215 + delta, 10)
    expect(geometry.http.end.x - geometry.http.start.x).toBeCloseTo(60 + delta, 10)
    const { reader, target } = geometry.actors
    const receiver = target.stages[0]
    expect(receiver.label.y - 12).toBeGreaterThanOrEqual(receiver.y + 4)
    expect(receiver.label.y + 3).toBeLessThanOrEqual(receiver.y + receiver.height - 4)
    expect(target.connectors[0].y1).toBe(receiver.y + receiver.height)
    expect(target.connectors[0].y2).toBeGreaterThan(target.connectors[0].y1)
    expect(reader.metrics.status.anchor).toBe('start')
    expect(reader.metrics.status.x).toBeGreaterThanOrEqual(reader.bounds.x)
    expect(reader.metrics.status.x).toBeGreaterThanOrEqual(16)
  })

  it('reserves a landscape row for upstream waiting labels', () => {
    const geometry = createPipelineGeometry({
      orientation: 'landscape',
      landscapeContentWidth: 1120,
      readerWorkers: 7,
      senderWorkers: 32,
    })
    const { reader, sender } = geometry.actors
    const workerStatusY = Math.max(reader.metrics.status.y, sender.metrics.status.y)
    for (const { metrics } of Object.values(geometry.channels)) {
      expect(metrics.waitingY - workerStatusY).toBeGreaterThanOrEqual(16)
    }
  })

  it.each([
    { reader: 1, sender: 1 },
    { reader: 7, sender: 1 },
    { reader: 7, sender: 7 },
    { reader: 7, sender: 8 },
    { reader: 7, sender: 16 },
    { reader: 7, sender: 32 },
  ])('derives portrait stages for Reader $reader and Sender $sender', ({ reader, sender }) => {
    const geometry = createPipelineGeometry({
      orientation: 'portrait',
      readerWorkers: reader,
      senderWorkers: sender,
    })
    const { reader: readerActor, throttler, sender: senderActor, target } = geometry.actors
    const ports = [
      readerActor.ports.output,
      throttler.ports.input,
      throttler.ports.output,
      senderActor.ports.input,
      senderActor.ports.output,
      target.ports.input,
    ]
    expect(ports.every((port) => port.x === 240)).toBe(true)
    for (let index = 1; index < ports.length; index += 1) {
      expect(ports[index]!.y).toBeGreaterThan(ports[index - 1]!.y)
    }
    expect(senderActor.title.y).toBeGreaterThan(throttler.bounds.y + throttler.bounds.height)
    expect(target.title.y).toBeGreaterThan(senderActor.bounds.bottom)
    expect(target.bounds.y + target.bounds.height).toBeLessThan(geometry.viewBox.height)
    for (const channel of Object.values(geometry.channels)) {
      expect(channel.metrics.throughputY).toBeGreaterThan(channel.start.y + 50)
      expect(channel.metrics.waitingY).toBeLessThan(channel.end.y - 80)
      expect(channel.end.y - channel.start.y).toBeGreaterThan(300)
    }
    expect(geometry.batchLabel.guard.y).toBeGreaterThan(
      geometry.channels['reader-to-throttler'].metrics.waitingY + 12,
    )
    expect(geometry.batchLabel.guard.y + geometry.batchLabel.guard.height).toBeLessThan(
      throttler.title.y,
    )
  })

  it('aligns landscape primary metrics below their actor bounds', () => {
    const { actors } = createPipelineGeometry({
      orientation: 'landscape',
      readerWorkers: 7,
      senderWorkers: 32,
    })
    expect(actors.reader.metrics.primary.y).toBe(actors.sender.metrics.primary.y)
    for (const actor of [actors.reader, actors.sender]) {
      expect(actor.metrics.primary.y).toBeGreaterThan(actor.bounds.bottom)
    }
    const { throttler } = actors
    for (const metric of [
      throttler.metrics.installed.requested,
      throttler.metrics.bypass.admitted,
    ]) {
      expect(metric.caption.y).toBeGreaterThan(throttler.bounds.y + throttler.bounds.height)
      expect(metric.value.y).toBeGreaterThan(metric.caption.y)
    }
  })

  it.each([true, false] as const)(
    'stacks portrait throttler %s metrics in the right telemetry rail',
    (installed) => {
      const geometry = createPipelineGeometry({
        orientation: 'portrait',
        readerWorkers: 7,
        senderWorkers: 32,
      })
      const throttler = geometry.actors.throttler
      if (!('portraitPipe' in throttler)) {
        throw new Error('portrait pipe geometry is missing')
      }
      const placements =
        installed === true
          ? [
              throttler.metrics.installed.requested.caption,
              throttler.metrics.installed.requested.value,
              throttler.metrics.installed.admitted.caption,
              throttler.metrics.installed.admitted.value,
            ]
          : [throttler.metrics.bypass.admitted.caption, throttler.metrics.bypass.admitted.value]
      const cardRight = throttler.bounds.x + throttler.bounds.width
      const cardBottom = throttler.bounds.y + throttler.bounds.height
      const pipeRight =
        Math.max(
          throttler.portraitPipe.input.start.x,
          throttler.portraitPipe.output.start.x,
          ...throttler.portraitPipe.input.commands.flatMap((command) =>
            command.kind === 'quadratic'
              ? [command.control.x, command.end.x]
              : command.kind === 'horizontal'
                ? [command.x]
                : [],
          ),
          ...throttler.portraitPipe.output.commands.flatMap((command) =>
            command.kind === 'quadratic'
              ? [command.control.x, command.end.x]
              : command.kind === 'horizontal'
                ? [command.x]
                : [],
          ),
        ) + throttler.transform.x

      for (const placement of placements) {
        const globalX = placement.x + throttler.transform.x
        const globalY = placement.y + throttler.transform.y

        expect(globalX).toBeGreaterThan(cardRight)
        expect(globalX).toBeGreaterThan(pipeRight)
        expect(globalX).toBeGreaterThan(throttler.ports.output.x)
        expect(globalY).toBeLessThan(cardBottom)
        expect(globalY).toBeGreaterThan(geometry.actors.throttler.bounds.y)
        expect(placement.anchor).toBe('start')
      }

      const globalPlacements = placements.map((placement) => ({
        x: placement.x + throttler.transform.x,
        y: placement.y + throttler.transform.y,
      }))
      for (let i = 1; i < globalPlacements.length; i += 1) {
        expect(globalPlacements[i].y).toBeGreaterThan(globalPlacements[i - 1].y)
        expect(globalPlacements[i].x).toBe(globalPlacements[0].x)
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
        const next =
          command.kind === 'horizontal'
            ? { x: command.x, y: current.y }
            : command.kind === 'vertical'
              ? { x: current.x, y: command.y }
              : command.end

        expect(next.y).toBeGreaterThanOrEqual(current.y)
        if (command.kind === 'quadratic') {
          curveCount += 1
          expect(command.control.y).toBeGreaterThanOrEqual(current.y)
          expect(command.end.y).toBeGreaterThanOrEqual(command.control.y)
          expect(Math.hypot(command.control.x - current.x, command.control.y - current.y)).toBe(16)
          expect(
            Math.hypot(command.end.x - command.control.x, command.end.y - command.control.y),
          ).toBe(16)
        }
        const horizontalDirection = Math.sign(next.x - current.x)
        if (horizontalDirection !== 0) {
          horizontalDirections.push(horizontalDirection)
        }
        current = next
      }

      const directionChanges = horizontalDirections
        .slice(1)
        .filter((direction, index) => direction !== horizontalDirections[index])
      expect(current).toEqual(path.end)
      expect(curveCount).toBe(3)
      expect(directionChanges.length).toBeLessThanOrEqual(1)
    }

    expect(globalPoint(input.start)).toEqual(throttler.ports.input)
    expect(input.end).toEqual({ x: VALVE_FLANGES.left, y: 415 })
    expect(output.start).toEqual({ x: VALVE_FLANGES.right, y: 415 })
    expect(globalPoint(output.end)).toEqual(throttler.ports.output)
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
): TChannel {
  return {
    ...channel,
    depthBatches: Math.min(applied, 4),
    inputTransactionsPerSecond: 50_000,
    outputTransactionsPerSecond: 50_000,
    capacity: applied,
  }
}

function activeSnapshot(): LoadgenSnapshot {
  const adapter = new SimulationAdapter()
  const base = adapter.getSnapshot()
  adapter.dispose()
  const telemetry: LoadgenTelemetrySnapshot = {
    ...base,
    runState: 'running',
    readerChannel: activeChannel(base.readerChannel, 10),
    senderChannel: activeChannel(base.senderChannel, 160),
  }
  return new ChannelFlowStateDeriver().derive(telemetry, 0)
}

function desiredControl<T>(applied: T): DesiredControl<T> {
  return {
    applied,
    desired: applied,
    phase: 'idle',
    error: null,
    available: true,
    preview: vi.fn(),
    cancel: vi.fn(),
    commit: vi.fn().mockResolvedValue(true),
  }
}

function liveControls(
  snapshot: LoadgenSnapshot,
  overrides: Partial<LiveControls> = {},
): LiveControls {
  return {
    readerWorkers: desiredControl(snapshot.reader.workers.applied ?? snapshot.reader.workers.min),
    requestedTps: desiredControl(
      snapshot.throttler.requestedTps.applied ?? snapshot.throttler.requestedTps.min,
    ),
    installed: desiredControl(snapshot.throttler.installed.applied ?? true),
    senderWorkers: desiredControl(snapshot.sender.workers.applied ?? snapshot.sender.workers.min),
    ...overrides,
  }
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
      liveControls={liveControls(snapshot)}
      geometry={geometry}
    />,
  )
}

function withWorkerCount(snapshot: LoadgenSnapshot, count: number): LoadgenSnapshot {
  const control = (workers: LoadgenSnapshot['reader']['workers']) => ({
    ...workers,
    applied: count,
    min: 0,
    max: 32,
  })

  return {
    ...snapshot,
    reader: {
      ...snapshot.reader,
      workers: control(snapshot.reader.workers),
      workerStates: Array.from({ length: count }, () => ({
        activity: 'idle' as const,
        draining: false,
      })),
    },
    sender: {
      ...snapshot.sender,
      workers: control(snapshot.sender.workers),
      workerStates: Array.from({ length: count }, () => ({
        activity: 'idle' as const,
        draining: false,
      })),
    },
  }
}

function pipelineElement(snapshot: LoadgenSnapshot) {
  return (
    <PipelineSvg
      snapshot={snapshot}
      selectedId={null}
      onSelect={vi.fn()}
      liveControls={liveControls(snapshot)}
      geometry={createPipelineGeometry({
        orientation: 'landscape',
        readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
        senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
      })}
    />
  )
}

describe('PipelineSvg rendering', () => {
  it.each(['landscape', 'portrait'] as const)(
    'renders one unified Batch above Throttler with shared hose width in %s',
    (orientation) => {
      const snapshot = activeSnapshot()
      const view = renderPipeline(snapshot, orientation)
      const controls = view.container.querySelectorAll('.pipeline-batch-size')
      const cables = view.container.querySelectorAll<SVGPathElement>('.pipeline-channel-cable')

      expect(controls).toHaveLength(1)
      expect(controls[0]?.textContent).toBe(
        `Batch ${snapshot.reader.readBatchSize?.toLocaleString('en-US')} tx`,
      )
      expect(
        view.container.querySelector('#channel-reader-to-throttler .pipeline-batch-size'),
      ).toBeNull()
      expect(
        view.container.querySelector('#channel-throttler-to-sender .pipeline-batch-size'),
      ).toBeNull()
      expect(cables).toHaveLength(2)
      expect(cables[0]?.style.strokeWidth).toBe(cables[1]?.style.strokeWidth)
    },
  )

  it('keeps rendered actor bottoms, ports and links stable across worker boundaries', () => {
    const base = activeSnapshot()
    const view = render(pipelineElement(withWorkerCount(base, 0)))
    let initial: unknown
    for (const count of [0, 2, 3, 7, 8, 16, 32]) {
      view.rerender(pipelineElement(withWorkerCount(base, count)))
      const actors = ['reader', 'sender'].map((id) => {
        const actor = view.container.querySelector(`#${id}-actor`)!
        const box = actor.querySelector('.pipeline-actor-box')!
        const top = Number(box.getAttribute('y'))
        const bottom = top + Number(box.getAttribute('height'))
        const ports = [...actor.querySelectorAll('.pipeline-port')].map((port) => ({
          x: Number(port.getAttribute('cx')),
          y: Number(port.getAttribute('cy')),
        }))
        expect(actor.getAttribute('data-worker-count')).toBe(String(count))
        for (const port of ports) {
          expect(port.y).toBeGreaterThan(top)
          expect(port.y).toBeLessThan(bottom)
        }
        return { bottom, ports }
      })
      const paths = [
        ...view.container.querySelectorAll('.pipeline-channel-cable, .pipeline-http-line'),
      ].map((path) => path.getAttribute('d'))
      expect(paths).toHaveLength(3)
      const actual = { actors, paths }
      if (initial === undefined) initial = actual
      else expect(actual).toEqual(initial)
    }
  })

  it.each(['landscape', 'portrait'] as const)(
    'shows Reader worker state summary under its rate in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        reader: {
          ...base.reader,
          readTps: 50_000,
          workerStates: [
            { activity: 'idle', draining: false },
            { activity: 'reading', draining: false },
            { activity: 'blocked', draining: false },
          ],
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const reader = view.container.querySelector('#reader-actor')

      expect(reader?.textContent).toContain('Read 50,000 tx/s')
      expect(reader?.textContent).toContain('0 errors')
      expect(reader?.querySelector('.pipeline-worker-status')?.textContent).toContain(
        '1 idle · 1 reading · 1 blocked · 0 errors',
      )
      expect(reader?.querySelector('.pipeline-worker-status .worker-state--idle')).toHaveProperty(
        'textContent',
        '1 idle',
      )
      expect(
        reader?.querySelector('.pipeline-worker-status .worker-state--in-flight'),
      ).toHaveProperty('textContent', ' · 1 reading')
      expect(
        reader?.querySelector('.pipeline-worker-status .worker-state--backoff'),
      ).toHaveProperty('textContent', ' · 1 blocked')
      expect(reader?.querySelector('.pipeline-worker-status .worker-state--error')).toHaveProperty(
        'textContent',
        ' · 0 errors',
      )
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'covers only the Reader actor bounds for a source error in %s',
    (orientation) => {
      const base = activeSnapshot()
      const view = renderPipeline(
        {
          ...base,
          reader: {
            ...base.reader,
            sourceError: {
              category: 'source',
              operation: 'read',
              relativePath: 'broken.parquet',
              message: 'corrupt parquet',
            },
          },
        },
        orientation,
      )
      const reader = view.container.querySelector('#reader-actor')!
      const box = reader.querySelector<SVGRectElement>('.pipeline-actor-box')!
      const overlay = reader.querySelector<SVGGElement>(
        '[data-testid="reader-source-error-overlay"]',
      )!
      const overlayRect = overlay.querySelector<SVGRectElement>('rect')!
      const overlayText = overlay.querySelector<SVGTextElement>('text')!

      expect(overlay.getAttribute('role')).toBe('alert')
      expect(getComputedStyle(overlayRect).fill).toBe('var(--reader-source-error-background)')
      expect(getComputedStyle(overlayRect).fillOpacity).toBe('0.84')
      expect(getComputedStyle(overlayText).fill).toBe('var(--reader-source-error-text)')
      expect({
        x: Number(overlayRect.getAttribute('x')),
        y: Number(overlayRect.getAttribute('y')),
        width: Number(overlayRect.getAttribute('width')),
        height: Number(overlayRect.getAttribute('height')),
      }).toEqual({
        x: Number(box.getAttribute('x')),
        y: Number(box.getAttribute('y')),
        width: Number(box.getAttribute('width')),
        height: Number(box.getAttribute('height')),
      })
      expect(Number(overlayText.getAttribute('x'))).toBe(
        Number(box.getAttribute('x')) + Number(box.getAttribute('width')) / 2,
      )
      expect(Number(overlayText.getAttribute('y'))).toBe(
        Number(box.getAttribute('y')) + Number(box.getAttribute('height')) / 2,
      )
      expect(
        view.container.querySelectorAll('[data-testid="reader-source-error-overlay"]'),
      ).toHaveLength(1)
      expect(
        view.container.querySelector('#sender-actor [data-testid="reader-source-error-overlay"]'),
      ).toBeNull()
      view.unmount()
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'renders Sender worker state summary as semantic segments in %s',
    (orientation) => {
      const base = activeSnapshot()
      const view = renderPipeline(
        {
          ...base,
          sender: {
            ...base.sender,
            workerStates: [
              { activity: 'idle', draining: false },
              { activity: 'inFlight', draining: false },
              { activity: 'backoff', draining: false },
            ],
          },
        },
        orientation,
      )
      const status = view.container.querySelector('#sender-actor .pipeline-worker-status')!

      expect(status.textContent).toContain('1 idle · 1 in-flight · 1 backoff')
      expect(status.querySelectorAll('.worker-state')).toHaveLength(3)
      expect(status.querySelector('.worker-state--idle')).toHaveProperty('textContent', '1 idle')
      expect(status.querySelector('.worker-state--in-flight')).toHaveProperty(
        'textContent',
        ' · 1 in-flight',
      )
      expect(status.querySelector('.worker-state--backoff')).toHaveProperty(
        'textContent',
        ' · 1 backoff',
      )
      view.unmount()
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'renders noninteractive static ingestion stages in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot = base
      const view = renderPipeline(snapshot, orientation)
      const target = view.container.querySelector('#target-actor')!
      expect(target.textContent).toContain('HTTP receiver')
      expect(target.hasAttribute('role')).toBe(false)
      expect(target.hasAttribute('tabindex')).toBe(false)
      expect(view.container.querySelector('#http-link')?.hasAttribute('tabindex')).toBe(false)
      expect(view.container.querySelector('#http-link')?.hasAttribute('role')).toBe(false)
      expect(view.container.querySelector('#http-link text')).toBeNull()
      expect(target.textContent).toContain('S3 / Vaultbox')
      expect(target.textContent).toContain('Postgres outbox')
      expect(target.textContent).toContain('Kafka')
      expect(target.querySelectorAll('.pipeline-service-stage')).toHaveLength(4)
      expect(target.querySelectorAll('.pipeline-service-connector')).toHaveLength(3)
    },
  )

  it('renders the default 4 and 100 batch hoses with one capacity scale', () => {
    const adapter = new SimulationAdapter()
    const snapshot = new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0)
    adapter.dispose()
    const view = renderPipeline(snapshot)
    for (const [id, capacity, legs] of [
      ['reader-to-throttler', 4, 4],
      ['throttler-to-sender', 100, 10],
    ] as const) {
      const channel = view.container.querySelector(`#channel-${id}`)!
      expect(channel.getAttribute('data-leg-count')).toBe(String(legs))
      expect(channel.textContent).toContain(`Depth 0 / ${capacity} batches`)
    }
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
    const senderChannel = view.container.querySelector<SVGGElement>('#channel-throttler-to-sender')!

    expect(valve.style.getPropertyValue('--pipeline-valve-flow-color')).toBe('#79d957')
    expect(senderChannel.style.getPropertyValue('--pipeline-channel-pressure-color')).toBe(
      '#ff6748',
    )
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
        },
      },
      readerChannel: {
        ...base.readerChannel,
        depthBatches: 0,
        outputTransactionsPerSecond: 0,
        flowState: 'normal',
        displayedPressure: 0,
      },
      senderChannel: {
        ...base.senderChannel,
        depthBatches: 0,
        outputTransactionsPerSecond: 0,
        flowState: 'normal',
        displayedPressure: 0,
      },
    }
    const view = renderPipeline(drained)
    const vacuum = view.container.querySelector<SVGEllipseElement>('.pipeline-valve-vacuum')!
    expect(vacuum.dataset.vacuumFill).toBe('uniform')
    expect(getComputedStyle(vacuum).fill).toBe('var(--valve-vacuum-fill)')
    const senderChannel = view.container.querySelector<SVGGElement>('#channel-throttler-to-sender')!
    expect(senderChannel.style.getPropertyValue('--pipeline-channel-pressure-color')).toBe(
      '#79d957',
    )
  })

  it('reserves portrait card height for live workers after downscale', () => {
    const base = activeSnapshot()
    const snapshot = {
      ...base,
      sender: {
        ...base.sender,
        workers: { ...base.sender.workers, applied: 1 },
        workerStates: Array.from({ length: 32 }, () => ({
          activity: 'idle' as const,
          draining: false,
        })),
      },
    }
    const view = render(
      <PipelineSvg
        snapshot={snapshot}
        selectedId={null}
        onSelect={vi.fn()}
        liveControls={liveControls(snapshot)}
        geometry={createPipelineGeometry({
          orientation: 'portrait',
          readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
          senderWorkers: 32,
        })}
      />,
    )
    const card = view.container.querySelector('#sender-actor .pipeline-actor-box')!
    const title = view.container.querySelector('.pipeline-title--sender')!
    const port = view.container.querySelector('#sender-actor .pipeline-port')!
    expect(Number(card.getAttribute('y'))).toBeGreaterThan(Number(title.getAttribute('y')) + 40)
    expect(Number(port.getAttribute('cy'))).toBe(Number(card.getAttribute('y')))
    expect(view.container.querySelector('#sender-actor')?.getAttribute('data-worker-rows')).toBe(
      '7',
    )
  })

  it('keeps the offset bypass cradle inside portrait and right of centerline', () => {
    const base = activeSnapshot()
    const snapshot: LoadgenSnapshot = {
      ...base,
      adapterKind: 'http',
      throttler: {
        ...base.throttler,
        installed: {
          ...base.throttler.installed,
          applied: false,
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
    const left = Number(target.getAttribute('x')) + geometry.actors.throttler.transform.x
    const right = left + Number(target.getAttribute('width'))

    expect(left).toBeGreaterThan(240)
    expect(right).toBeLessThanOrEqual(480)
  })

  it.each(['landscape', 'portrait'] as const)(
    'renders deterministic Sender in-flight, backoff, and idle states in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        sender: {
          ...base.sender,
          workerStates: [
            { activity: 'idle', draining: false },
            { activity: 'inFlight', draining: false },
            { activity: 'backoff', draining: false },
          ],
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const sender = view.container.querySelector('#sender-actor')!

      expect(sender.getAttribute('aria-label')).toBe(
        'Inspect sender, 3 desired, 3 live, 0 draining',
      )
      expect(sender.querySelectorAll('.pipeline-worker--in-flight')).toHaveLength(1)
      expect(sender.querySelectorAll('.pipeline-worker--backoff')).toHaveLength(1)
      expect(sender.querySelectorAll('.pipeline-worker--idle')).toHaveLength(1)
      expect(
        [...sender.querySelectorAll('[class*="pipeline-worker--"]')].map((chip) =>
          chip.getAttribute('class'),
        ),
      ).toEqual(['pipeline-worker--backoff', 'pipeline-worker--in-flight', 'pipeline-worker--idle'])
      expect(
        getComputedStyle(sender.querySelector('.pipeline-worker--backoff .pipeline-worker-led')!)
          .fill,
      ).toBe('var(--orange)')
    },
  )

  it.each(['starting', 'stopping'] as const)(
    'keeps desired counts without worker markers for known empty pools in %s',
    (state) => {
      const base = activeSnapshot()
      const zeroFlowChannel = (channel: typeof base.readerChannel) => ({
        ...channel,
        depthBatches: 0,
        inputTransactionsPerSecond: 0,
        outputTransactionsPerSecond: 0,
        displayedPressure: 0,
        flowState: 'normal' as const,
      })
      const snapshot: LoadgenSnapshot = {
        ...base,
        runState: state,
        reader: {
          ...base.reader,
          workers: { ...base.reader.workers, applied: 2 },
          workerStates: [],
          readBatchSize: 1_500,
        },
        sender: {
          ...base.sender,
          workers: { ...base.sender.workers, applied: 3 },
          workerStates: [],
        },
        readerChannel: zeroFlowChannel(base.readerChannel),
        senderChannel: zeroFlowChannel(base.senderChannel),
      }
      const view = renderPipeline(snapshot)

      expect(view.container.querySelector('#reader-actor')?.getAttribute('data-worker-count')).toBe(
        '2',
      )
      expect(view.container.querySelector('#sender-actor')?.getAttribute('data-worker-count')).toBe(
        '3',
      )
      expect(view.container.querySelectorAll('#reader-actor .pipeline-worker-chip')).toHaveLength(0)
      expect(view.container.querySelectorAll('#sender-actor .pipeline-worker-chip')).toHaveLength(0)
      expect(view.getAllByText('Batch 1,500 tx')).toHaveLength(1)
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'shows aggregate draining markers beyond desired count in %s',
    (orientation) => {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        sender: {
          ...base.sender,
          workers: { ...base.sender.workers, applied: 1 },
          workerStates: [
            { activity: 'idle', draining: true },
            { activity: 'inFlight', draining: false },
            { activity: 'backoff', draining: true },
          ],
        },
      }
      const view = renderPipeline(snapshot, orientation)
      const sender = view.container.querySelector('#sender-actor')!

      expect(sender.getAttribute('data-worker-count')).toBe('1')
      expect(sender.getAttribute('aria-label')).toBe(
        'Inspect sender, 1 desired, 3 live, 2 draining',
      )
      expect(sender.querySelectorAll('[class*="pipeline-worker--"]')).toHaveLength(3)
      expect(
        [...sender.querySelectorAll('[class*="pipeline-worker--"]')].map((chip) =>
          chip.getAttribute('class'),
        ),
      ).toEqual([
        'pipeline-worker--backoff pipeline-worker--draining',
        'pipeline-worker--in-flight',
        'pipeline-worker--idle pipeline-worker--draining',
      ])
      const draining = sender.querySelector('.pipeline-worker--draining')!
      expect(draining.classList).toContain('pipeline-worker--draining')
      expect(getComputedStyle(draining.querySelector('.pipeline-worker-led')!).fill).toBe(
        'var(--purple)',
      )
      expect(view.container.querySelector('#sender-count')?.textContent).toBe('1')
      view.unmount()
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'renders Reader aggregate state priority and hatch in %s',
    (orientation) => {
      const base = activeSnapshot()
      const view = renderPipeline(
        {
          ...base,
          reader: {
            ...base.reader,
            workers: { ...base.reader.workers, applied: 1 },
            workerStates: [
              { activity: 'idle', draining: true },
              { activity: 'reading', draining: true },
              { activity: 'reading', draining: false },
              { activity: 'blocked', draining: true },
              { activity: 'blocked', draining: false },
            ],
          },
        },
        orientation,
      )
      const reader = view.container.querySelector('#reader-actor')!
      expect(reader.getAttribute('aria-label')).toBe('Inspect reader')
      expect(reader.querySelectorAll('[class*="pipeline-worker--"]')).toHaveLength(5)
      expect(
        [...reader.querySelectorAll('[class*="pipeline-worker--"]')].map((chip) =>
          chip.getAttribute('class'),
        ),
      ).toEqual([
        'pipeline-worker--blocked pipeline-worker--draining',
        'pipeline-worker--blocked',
        'pipeline-worker--reading pipeline-worker--draining',
        'pipeline-worker--reading',
        'pipeline-worker--idle pipeline-worker--draining',
      ])
      expect(getComputedStyle(reader.querySelector('.pipeline-worker--idle')!).opacity).toBe('0.34')
      view.unmount()
    },
  )

  it.each([
    ['reader', 'landscape'],
    ['sender', 'portrait'],
  ] as const)(
    'shows 32 aggregate worker markers without overflow for %s in %s',
    (actor, orientation) => {
      const base = activeSnapshot()
      const view = renderPipeline(
        {
          ...base,
          [actor]: {
            ...base[actor],
            workers: { ...base.sender.workers, applied: 32 },
            workerStates:
              actor === 'reader'
                ? [
                    ...Array.from({ length: 25 }, () => ({
                      activity: 'idle' as const,
                      draining: false,
                    })),
                    ...Array.from({ length: 4 }, () => ({
                      activity: 'reading' as const,
                      draining: false,
                    })),
                    ...Array.from({ length: 3 }, () => ({
                      activity: 'blocked' as const,
                      draining: false,
                    })),
                  ]
                : [
                    ...Array.from({ length: 25 }, () => ({
                      activity: 'idle' as const,
                      draining: false,
                    })),
                    ...Array.from({ length: 4 }, () => ({
                      activity: 'inFlight' as const,
                      draining: false,
                    })),
                    ...Array.from({ length: 3 }, () => ({
                      activity: 'backoff' as const,
                      draining: false,
                    })),
                  ],
          },
        },
        orientation,
      )
      const workerActor = view.container.querySelector(`#${actor}-actor`)!

      expect(workerActor.querySelectorAll('[class*="pipeline-worker--"]')).toHaveLength(32)
      expect(workerActor.textContent).not.toContain('+')
      view.unmount()
    },
  )

  it.each(['landscape', 'portrait'] as const)(
    'fills the available room for large live pools and centers their overflow in %s',
    (orientation) => {
      const base = activeSnapshot()
      const live = 1_234
      const pool = {
        workers: { ...base.sender.workers, max: 10_000, applied: 10_000 },
        workerStates: Array.from({ length: live }, () => ({
          activity: 'idle' as const,
          draining: false,
        })),
      }
      const view = renderPipeline(
        {
          ...base,
          reader: { ...base.reader, ...pool },
          sender: { ...base.sender, ...pool },
        },
        orientation,
      )
      for (const id of ['reader', 'sender']) {
        const actor = view.container.querySelector(`#${id}-actor`)!
        const box = actor.querySelector('.pipeline-actor-box')!
        const drawn = actor.querySelectorAll('.pipeline-worker-chip').length
        expect(drawn).toBeGreaterThan(32)
        expect(drawn).toBeLessThan(live)
        expect(Number(box.getAttribute('height'))).toBeLessThanOrEqual(305)
        const overflow = actor.querySelector('.pipeline-worker-overflow')!
        expect(overflow.textContent).toBe(`+${live - drawn} workers`)
        expect(overflow.getAttribute('text-anchor')).toBe('middle')
        expect(Number(overflow.getAttribute('x'))).toBe(
          Number(box.getAttribute('x')) + Number(box.getAttribute('width')) / 2,
        )
        const bottom = Number(box.getAttribute('y')) + Number(box.getAttribute('height'))
        expect(bottom - Number(overflow.getAttribute('y'))).toBeGreaterThanOrEqual(7)
        expect(bottom - Number(overflow.getAttribute('y'))).toBeLessThanOrEqual(22)
      }
      view.unmount()
    },
  )

  it('keeps an aggregate backoff marker static amber with reduced motion', () => {
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
    matchMedia.mockImplementation((query) => ({
      ...mediaQueryList(query, query === '(prefers-reduced-motion: reduce)'),
    }))

    try {
      const base = activeSnapshot()
      const snapshot: LoadgenSnapshot = {
        ...base,
        sender: {
          ...base.sender,
          workerStates: [
            { activity: 'idle', draining: false },
            { activity: 'idle', draining: false },
            { activity: 'backoff', draining: false },
          ],
        },
      }
      const view = renderPipeline(snapshot)
      const backoff = view.container.querySelector('.pipeline-worker--backoff')!

      expect(backoff.classList).toContain('pipeline-worker--backoff')
      expect(getComputedStyle(backoff).animationName).toBe('none')
      expect(getComputedStyle(backoff.querySelector('.pipeline-worker-led')!).fill).toBe(
        'var(--orange)',
      )
    } finally {
      matchMedia.mockImplementation((query) => mediaQueryList(query, false))
    }
  })
})
