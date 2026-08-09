/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type { QueueSnapshot, SelectableId } from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import { QUEUE_CABLE_ENDPOINTS } from './geometry'
import type { QueueMarkerSlotSnapshot } from './markerLifecycle'
import {
  buildQueueCablePath,
  capacityToCableY,
  QUEUE_CABLE_MAX_LIFT,
} from './queueCableGeometry'
import { QueueCable } from './QueueCable'

const styleElement = document.createElement('style')
const pipelineStyles = readFileSync(
  resolve(process.cwd(), 'src/components/pipeline/PipelineSvg.css'),
  'utf8',
)

beforeAll(() => {
  styleElement.textContent = pipelineStyles
  document.head.append(styleElement)
})

afterAll(() => {
  styleElement.remove()
})

function derivedSnapshot(adapter: SimulationAdapter) {
  return new QueueFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}

function queueSnapshot(
  queue: QueueSnapshot,
  applied: number,
  candidate: number | null = null,
): QueueSnapshot {
  return {
    ...queue,
    depthBatches: applied,
    capacity: {
      ...queue.capacity,
      applied,
      preview: candidate,
      pending: candidate,
    },
  }
}

function marker(
  queue: QueueSnapshot,
  kind: QueueMarkerSlotSnapshot['kind'],
): QueueMarkerSlotSnapshot {
  return {
    slotId: `${queue.id}-${kind}-test`,
    familyId: `${kind}-family`,
    queueId: queue.id,
    kind,
    state: 'active',
    phase: kind === 'occupancy' ? 0.25 : 0.75,
    queued: kind === 'occupancy',
  }
}

function renderCable(
  snapshot: QueueSnapshot,
  options: {
    markers?: readonly QueueMarkerSlotSnapshot[]
    onSelect?: (id: SelectableId) => void
    onCapacityChange?: (id: QueueSnapshot['id'], value: number) => void
  } = {},
) {
  const endpoints = QUEUE_CABLE_ENDPOINTS[snapshot.id]
  return render(
    <svg viewBox="0 0 1100 640">
      <QueueCable
        snapshot={snapshot}
        start={endpoints.start}
        end={endpoints.end}
        selected={false}
        markers={options.markers ?? []}
        onSelect={options.onSelect ?? (() => undefined)}
        onCapacityChange={options.onCapacityChange ?? (() => undefined)}
      />
    </svg>,
  )
}

function installSvgCoordinates(svg: SVGSVGElement) {
  Object.defineProperty(svg, 'getScreenCTM', {
    configurable: true,
    value: () => ({ inverse: () => ({}) }),
  })
  Object.defineProperty(svg, 'createSVGPoint', {
    configurable: true,
    value: () => {
      const point = {
        x: 0,
        y: 0,
        matrixTransform: () => ({ x: point.x, y: point.y }),
      }
      return point
    },
  })
}

function pathApexY(path: string): number {
  const quadraticCommands = [...path.matchAll(
    /Q-?\d+(?:\.\d+)? (-?\d+(?:\.\d+)?) -?\d+(?:\.\d+)? (-?\d+(?:\.\d+)?)/g,
  )]
  if (quadraticCommands.length === 0) {
    throw new Error(`expected a lifted queue cable path, received: ${path}`)
  }

  return Math.min(...quadraticCommands.flatMap((command) => [
    Number(command[1]),
    Number(command[2]),
  ]))
}

function translatedY(element: Element): number {
  const transform = element.getAttribute('transform')
  const match = transform?.match(
    /^translate\(-?\d+(?:\.\d+)? (-?\d+(?:\.\d+)?)\)$/,
  )
  if (match === undefined || match === null) {
    throw new Error(`expected translate transform, received: ${transform}`)
  }

  return Number(match[1])
}

describe('QueueCable mounted behavior', () => {
  it('uses keyboard capacity steps and bounds for both queue scales', async () => {
    const user = userEvent.setup()
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      { queue: snapshot.queue1, expected: [5, 9, 0, 12] },
      { queue: snapshot.queue2, expected: [110, 150, 0, 160] },
    ] as const

    for (const testCase of cases) {
      const onCapacityChange = vi.fn()
      const view = renderCable(testCase.queue, { onCapacityChange })
      const slider = screen.getByRole('slider')

      await user.click(slider)
      await user.keyboard('{ArrowUp}{PageUp}{Home}{End}')

      expect(onCapacityChange.mock.calls.map((call) => call[1])).toEqual(
        testCase.expected,
      )
      view.unmount()
    }
    adapter.dispose()
  })

  it('shows a local pointer preview and commits the released capacity', () => {
    const adapter = new SimulationAdapter()
    const queue = queueSnapshot(derivedSnapshot(adapter).queue1, 4)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(queue, { onCapacityChange })
    installSvgCoordinates(container.querySelector('svg')!)
    const slider = screen.getByRole('slider')
    const solidPath = container.querySelector('.pipeline-queue-cable')!
    const appliedPath = solidPath.getAttribute('d')

    fireEvent.pointerDown(slider, {
      pointerId: 7,
      button: 0,
      clientY: 300,
    })
    fireEvent.pointerMove(slider, { pointerId: 7, clientY: 260 })

    const ghost = container.querySelector('.pipeline-queue-requested-cable')!
    const handleBody = slider.querySelector('.pipeline-queue-handle__body')!
    const handleValue = slider.querySelector('.pipeline-queue-handle__value')!
    const requestRing = slider.querySelector(
      '.pipeline-queue-handle__request-ring',
    )!
    const appliedStyle = getComputedStyle(solidPath)
    const ghostStyle = getComputedStyle(ghost)
    const handleStyle = getComputedStyle(slider)
    expect(ghost).not.toBeNull()
    expect(ghost.classList).toContain('pipeline-queue-requested-cable--preview')
    expect(ghostStyle.stroke).toBe('var(--cyan)')
    expect(ghostStyle.strokeWidth).toBe('2px')
    expect(ghostStyle.strokeDasharray).toBe('none')
    expect(ghostStyle.opacity).toBe('0.92')
    expect(Number.parseFloat(ghostStyle.strokeWidth)).toBeLessThan(
      Number.parseFloat(appliedStyle.strokeWidth),
    )
    expect(handleStyle.getPropertyValue(
      '--pipeline-queue-handle-state-color',
    )).toBe('var(--cyan)')
    expect(getComputedStyle(handleBody).stroke).toBe(
      'var(--pipeline-queue-handle-state-color)',
    )
    expect(getComputedStyle(handleValue).fill).toBe(
      'var(--pipeline-queue-handle-state-color)',
    )
    expect(getComputedStyle(requestRing).stroke).toBe('var(--cyan)')
    expect(getComputedStyle(requestRing).opacity).toBe('1')
    expect(translatedY(slider)).toBe(pathApexY(ghost.getAttribute('d')!))
    expect(solidPath.getAttribute('d')).toBe(appliedPath)
    expect(slider.getAttribute('aria-valuenow')).toBe('6')

    fireEvent.pointerUp(slider, { pointerId: 7, clientY: 260 })
    expect(onCapacityChange).toHaveBeenCalledWith(queue.id, 6)
    expect(container.querySelector('.pipeline-queue-requested-cable')).toBeNull()
    adapter.dispose()
  })

  it('commits exactly once after release outside the moving handle', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      {
        queue: queueSnapshot(snapshot.queue1, 5),
        pointerId: 11,
        releaseY: 240,
        expected: 8,
      },
      {
        queue: queueSnapshot(snapshot.queue2, 30),
        pointerId: 12,
        releaseY: 285,
        expected: 40,
      },
    ] as const

    for (const testCase of cases) {
      const onCapacityChange = vi.fn()
      const view = renderCable(testCase.queue, { onCapacityChange })
      installSvgCoordinates(view.container.querySelector('svg')!)
      const slider = screen.getByRole('slider')

      fireEvent.pointerDown(slider, {
        pointerId: testCase.pointerId,
        button: 0,
        clientY: 300,
      })
      fireEvent.pointerMove(document.body, {
        pointerId: testCase.pointerId,
        clientY: testCase.releaseY,
      })

      expect(slider.getAttribute('aria-valuenow')).toBe(
        String(testCase.expected),
      )
      expect(slider.classList).toContain('pipeline-queue-handle--preview')

      fireEvent.pointerUp(document.body, {
        pointerId: testCase.pointerId,
        clientY: testCase.releaseY,
      })
      fireEvent.lostPointerCapture(slider, {
        pointerId: testCase.pointerId,
      })
      fireEvent.pointerUp(window, {
        pointerId: testCase.pointerId,
        clientY: testCase.releaseY,
      })

      expect(onCapacityChange).toHaveBeenCalledTimes(1)
      expect(onCapacityChange).toHaveBeenCalledWith(
        testCase.queue.id,
        testCase.expected,
      )
      expect(slider.classList).not.toContain('pipeline-queue-handle--preview')
      view.unmount()
    }
    adapter.dispose()
  })

  it('cancels on lost capture, pointercancel, and Escape', () => {
    const adapter = new SimulationAdapter()
    const queue = queueSnapshot(derivedSnapshot(adapter).queue2, 100, 30)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(queue, { onCapacityChange })
    installSvgCoordinates(container.querySelector('svg')!)
    const slider = screen.getByRole('slider')

    for (const terminalEvent of ['lostcapture', 'pointercancel', 'escape']) {
      fireEvent.pointerDown(slider, {
        pointerId: 13,
        button: 0,
        clientY: 300,
      })
      fireEvent.pointerMove(document.body, {
        pointerId: 13,
        clientY: 285,
      })
      expect(slider.classList).toContain('pipeline-queue-handle--preview')

      if (terminalEvent === 'lostcapture') {
        fireEvent.lostPointerCapture(slider, { pointerId: 13 })
      } else if (terminalEvent === 'pointercancel') {
        fireEvent.pointerCancel(document.body, { pointerId: 13 })
      } else {
        fireEvent.keyDown(window, { key: 'Escape' })
      }

      fireEvent.pointerUp(document.body, { pointerId: 13, clientY: 285 })
      expect(onCapacityChange).not.toHaveBeenCalled()
      expect(slider.classList).not.toContain('pipeline-queue-handle--preview')
      expect(slider.getAttribute('aria-valuenow')).toBe('100')
    }
    adapter.dispose()
  })

  it('removes active drag listeners on unmount', () => {
    const adapter = new SimulationAdapter()
    const queue = queueSnapshot(derivedSnapshot(adapter).queue2, 100, 30)
    const onCapacityChange = vi.fn()
    const view = renderCable(queue, { onCapacityChange })
    installSvgCoordinates(view.container.querySelector('svg')!)
    const slider = screen.getByRole('slider')

    fireEvent.pointerDown(slider, {
      pointerId: 14,
      button: 0,
      clientY: 300,
    })
    fireEvent.pointerMove(document.body, {
      pointerId: 14,
      clientY: 285,
    })
    view.unmount()
    fireEvent.pointerUp(document.body, { pointerId: 14, clientY: 285 })

    expect(onCapacityChange).not.toHaveBeenCalled()
    adapter.dispose()
  })

  it('cancels a pointer candidate without dispatching it', () => {
    const adapter = new SimulationAdapter()
    const queue = queueSnapshot(derivedSnapshot(adapter).queue1, 4)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(queue, { onCapacityChange })
    installSvgCoordinates(container.querySelector('svg')!)
    const slider = screen.getByRole('slider')

    fireEvent.pointerDown(slider, {
      pointerId: 9,
      button: 0,
      clientY: 300,
    })
    fireEvent.pointerMove(slider, { pointerId: 9, clientY: 260 })
    expect(container.querySelector('.pipeline-queue-requested-cable')).not.toBeNull()

    fireEvent.pointerCancel(slider, { pointerId: 9 })
    expect(onCapacityChange).not.toHaveBeenCalled()
    expect(container.querySelector('.pipeline-queue-requested-cable')).toBeNull()
    expect(slider.getAttribute('aria-valuenow')).toBe('4')
    adapter.dispose()
  })

  it('keeps applied cable, markers, label, and z-order stable for pending values', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      { queue: snapshot.queue1, applied: 12, candidate: 5 },
      { queue: snapshot.queue2, applied: 100, candidate: 30 },
    ] as const

    for (const testCase of cases) {
      const snapshot = queueSnapshot(
        testCase.queue,
        testCase.applied,
        testCase.candidate,
      )
      const endpoints = QUEUE_CABLE_ENDPOINTS[snapshot.id]
      const appliedY = capacityToCableY(
        testCase.applied,
        snapshot.capacity,
        endpoints.start.y,
        QUEUE_CABLE_MAX_LIFT,
      )
      const appliedPath = buildQueueCablePath(
        endpoints.start,
        endpoints.end,
        appliedY,
      )
      const view = renderCable(snapshot, {
        markers: [marker(snapshot, 'occupancy'), marker(snapshot, 'flow')],
      })
      const queueGroup = view.container.querySelector(`#queue-${snapshot.id}`)!
      const cable = queueGroup.querySelector('.pipeline-queue-cable')!
      const markerGroup = queueGroup.querySelector('.pipeline-queue-markers')!
      const markers = [...markerGroup.querySelectorAll<SVGCircleElement>('.pipeline-marker')]
      const appliedLabel = queueGroup.querySelector('.pipeline-queue-capacity-applied')!
      const metricLabels = [...queueGroup.querySelectorAll('.pipeline-queue-metric')]
      const slider = queueGroup.querySelector('.pipeline-queue-handle')!
      const handleBody = slider.querySelector('.pipeline-queue-handle__body')!
      const handleValue = slider.querySelector('.pipeline-queue-handle__value')!
      const children = [...queueGroup.children]
      const cableStyle = getComputedStyle(cable)
      const handleStyle = getComputedStyle(slider)

      expect(cable.getAttribute('d')).toBe(appliedPath)
      expect(queueGroup.querySelector('.pipeline-queue-requested-cable')).toBeNull()
      expect(Number.parseFloat(cableStyle.strokeWidth)).toBeGreaterThan(0)
      expect(handleStyle.getPropertyValue(
        '--pipeline-queue-handle-state-color',
      )).toBe('currentColor')
      expect(getComputedStyle(handleBody).stroke).toBe(
        'var(--pipeline-queue-handle-state-color)',
      )
      expect(getComputedStyle(handleValue).fill).toBe(
        'var(--pipeline-queue-handle-state-color)',
      )
      expect(translatedY(slider)).toBe(appliedY)
      expect(markers.map((item) => item.style.offsetPath)).toEqual([
        `path("${appliedPath}")`,
        `path("${appliedPath}")`,
      ])
      expect(getComputedStyle(markers[0]).offsetPath).toBe(
        `path("${appliedPath}")`,
      )
      expect(markers[0].classList).toContain('pipeline-marker--occupancy')
      expect(markers[1].classList).toContain('pipeline-marker--flow')
      expect(appliedLabel.getAttribute('transform')).toBe(
        `translate(${(endpoints.start.x + endpoints.end.x) / 2 - 50} ${appliedY})`,
      )
      expect(appliedLabel.textContent).toContain(`Applied ${testCase.applied}`)
      expect(slider.getAttribute('transform')).toBe(
        `translate(${(endpoints.start.x + endpoints.end.x) / 2} ${appliedY})`,
      )
      expect(children.indexOf(cable)).toBeLessThan(children.indexOf(markerGroup))
      expect(children.indexOf(markerGroup)).toBeLessThan(children.indexOf(appliedLabel))
      expect(children.indexOf(appliedLabel)).toBeLessThan(
        children.indexOf(metricLabels[0]),
      )
      expect(children.indexOf(metricLabels.at(-1)!)).toBeLessThan(
        children.indexOf(slider),
      )
      view.unmount()
    }
    adapter.dispose()
  })

  it('removes pending indicators after apply', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter).queue1
    const pending = queueSnapshot(base, 10, 4)
    const applied = queueSnapshot(base, 4)
    const endpoints = QUEUE_CABLE_ENDPOINTS[base.id]
    const view = renderCable(pending)

    expect(view.container.querySelector('.pipeline-queue-requested-cable')).toBeNull()
    expect(screen.getByText('Applied 10')).not.toBeNull()
    expect(screen.getByText('Pending 4 batches')).not.toBeNull()

    view.rerender(
      <svg viewBox="0 0 1100 640">
        <QueueCable
          snapshot={applied}
          start={endpoints.start}
          end={endpoints.end}
          selected={false}
          markers={[]}
          onSelect={() => undefined}
          onCapacityChange={() => undefined}
        />
      </svg>,
    )

    expect(view.container.querySelector('.pipeline-queue-requested-cable')).toBeNull()
    expect(screen.queryByText(/Applied 10/)).toBeNull()
    expect(screen.queryByText(/Pending 4 batches/)).toBeNull()
    expect(view.container.querySelector('.pipeline-queue-cable')?.getAttribute('d'))
      .toBe(buildQueueCablePath(endpoints.start, endpoints.end, 335))
    adapter.dispose()
  })

  it('selects the queue by click and Enter', async () => {
    const user = userEvent.setup()
    const adapter = new SimulationAdapter()
    const queue = {
      ...derivedSnapshot(adapter).queue1,
      displayedPressure: 0.5,
      flowState: 'near-limit' as const,
    }
    const onSelect = vi.fn()
    renderCable(queue, { onSelect })
    const control = screen.getByRole('button', {
      name: 'Inspect reader to throttler queue',
    })

    expect(control.getAttribute('data-pressure')).toBe('0.50')
    expect(control.getAttribute('style')).toContain(
      '--pipeline-queue-pressure-color: #ffd31f',
    )
    expect(control.querySelector('.pipeline-queue-cable')?.getAttribute('stroke'))
      .toBeNull()
    expect(getComputedStyle(screen.getByRole('slider')).getPropertyValue(
      '--pipeline-queue-handle-state-color',
    )).toBe('currentColor')

    await user.click(control)
    control.focus()
    await user.keyboard('{Enter}')

    expect(onSelect).toHaveBeenNthCalledWith(1, queue.id)
    expect(onSelect).toHaveBeenNthCalledWith(2, queue.id)
    adapter.dispose()
  })

  it('applies stopped and connection-error cable overrides immediately', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter).queue1
    const endpoints = QUEUE_CABLE_ENDPOINTS[base.id]
    const view = renderCable({
      ...base,
      displayedPressure: 1,
      flowState: 'stopped',
    })
    let queueGroup = view.container.querySelector(`#queue-${base.id}`)!

    expect(queueGroup.classList).toContain('pipeline-queue--stopped')
    expect(getComputedStyle(queueGroup).color).toBe('var(--muted)')
    expect(getComputedStyle(queueGroup).transition).toBe('none')

    view.rerender(
      <svg viewBox="0 0 1100 640">
        <QueueCable
          snapshot={{
            ...base,
            displayedPressure: 0,
            flowState: 'connection-error',
          }}
          start={endpoints.start}
          end={endpoints.end}
          selected={false}
          markers={[]}
          onSelect={() => undefined}
          onCapacityChange={() => undefined}
        />
      </svg>,
    )
    queueGroup = view.container.querySelector(`#queue-${base.id}`)!
    const slider = queueGroup.querySelector('.pipeline-queue-handle')!
    const handleBody = slider.querySelector('.pipeline-queue-handle__body')!
    expect(queueGroup.classList).toContain('pipeline-queue--connection-error')
    expect(getComputedStyle(queueGroup).color).toBe('var(--red)')
    expect(getComputedStyle(queueGroup).transition).toBe('none')
    expect(getComputedStyle(slider).getPropertyValue(
      '--pipeline-queue-handle-state-color',
    )).toBe('currentColor')
    expect(getComputedStyle(handleBody).strokeDasharray).toBe('6 3')
    adapter.dispose()
  })
})
