/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type { ChannelSnapshot, SelectableId } from '../../model/loadgen'
import { ChannelFlowStateDeriver } from '../../model/channelFlowState'
import { CHANNEL_CABLE_ENDPOINTS } from './geometry'
import {
  buildChannelCablePath,
  capacityToCableY,
  CHANNEL_CABLE_MAX_LIFT,
} from './channelCableGeometry'
import { ChannelCable } from './ChannelCable'

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
  return new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}

function channelSnapshot(
  channel: ChannelSnapshot,
  applied: number,
  candidate: number | null = null,
): ChannelSnapshot {
  return {
    ...channel,
    depthBatches: applied,
    capacity: {
      ...channel.capacity,
      applied,
      preview: candidate,
      pending: candidate,
    },
  }
}

function renderCable(
  snapshot: ChannelSnapshot,
  options: {
    onSelect?: (id: SelectableId) => void
    onCapacityChange?: (id: ChannelSnapshot['id'], value: number) => void
    capacityValues?: readonly number[]
  } = {},
) {
  const endpoints = CHANNEL_CABLE_ENDPOINTS[snapshot.id]
  return render(
    <svg viewBox="0 0 1100 640">
      <ChannelCable
        snapshot={snapshot}
        start={endpoints.start}
        end={endpoints.end}
        selected={false}
        onSelect={options.onSelect ?? (() => undefined)}
        onCapacityChange={options.onCapacityChange ?? (() => undefined)}
        capacityValues={options.capacityValues}
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
    throw new Error(`expected a lifted channel cable path, received: ${path}`)
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

describe('ChannelCable mounted behavior', () => {
  it('activates both channel cables only from their measured rates', () => {
    const adapter = new SimulationAdapter()
    const channel = derivedSnapshot(adapter).readerChannel
    const active = {
      ...channel,
      flowState: 'stopped' as const,
      capacity: { ...channel.capacity, applied: 0 },
      depthBatches: 0,
      blockedSenders: 0,
      blockedMs: 900,
      inputTransactionsPerSecond: 500,
      outputTransactionsPerSecond: 0,
    }
    const activeView = renderCable(active)
    const activeGroup = activeView.container.querySelector(
      '#channel-reader-to-throttler',
    )!

    expect(activeGroup.classList.contains('pipeline-channel--flow-active')).toBe(true)
    expect(activeGroup.getAttribute('data-input-active')).toBe('true')
    expect(activeGroup.getAttribute('data-output-active')).toBe('false')
    expect(activeView.container.textContent).not.toContain('Waiting upstream')
    activeView.unmount()

    const idleView = renderCable({
      ...active,
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 0,
    })
    const idleGroup = idleView.container.querySelector(
      '#channel-reader-to-throttler',
    )!
    expect(idleGroup.classList.contains('pipeline-channel--flow-active')).toBe(false)
    idleView.unmount()

    const senderActiveView = renderCable({
      ...active,
      id: 'throttler-to-sender',
      from: 'throttler',
      to: 'sender',
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 500,
    })
    const senderActiveGroup = senderActiveView.container.querySelector(
      '#channel-throttler-to-sender',
    )!
    expect(senderActiveGroup.classList.contains('pipeline-channel--flow-active')).toBe(true)
    senderActiveView.unmount()
    adapter.dispose()
  })

  it('uses keyboard capacity steps and bounds for both channel scales', async () => {
    const user = userEvent.setup()
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      { channel: snapshot.readerChannel, expected: [5, 9, 0, 12] },
      { channel: snapshot.senderChannel, expected: [110, 150, 0, 160] },
    ] as const

    for (const testCase of cases) {
      const onCapacityChange = vi.fn()
      const view = renderCable(testCase.channel, { onCapacityChange })
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

  it('shows and selects readerChannel HTTP capacities by discrete scale index', async () => {
    const user = userEvent.setup()
    const adapter = new SimulationAdapter()
    const source = derivedSnapshot(adapter).readerChannel
    const channel = {
      ...source,
      capacity: {
        ...source.capacity,
        applied: 2,
        min: 0,
        max: 8_192,
        step: 1,
        applyMode: 'immediate' as const,
      },
    }
    const onCapacityChange = vi.fn()
    const view = renderCable(channel, {
      onCapacityChange,
      capacityValues: [0, 1, 2, 8, 64, 512, 8_192],
    })
    const slider = screen.getByRole('slider')

    expect(slider.textContent).toBe('2')
    expect([...view.container.querySelectorAll(
      '.pipeline-channel-scale__label',
    )].map((label) => label.textContent)).toEqual(['0', '8', '8,192'])

    await user.click(slider)
    await user.keyboard('{ArrowUp}{PageUp}{Home}{End}')

    expect(onCapacityChange.mock.calls.map((call) => call[1]))
      .toEqual([8, 8_192, 0, 8_192])
    adapter.dispose()
  })

  it('makes both unavailable capacity handles gray and inert', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const channels = [snapshot.readerChannel, snapshot.senderChannel]

    for (const channel of channels) {
      const onCapacityChange = vi.fn()
      const view = renderCable({
        ...channel,
        capacity: { ...channel.capacity, applyMode: 'unavailable' },
      }, { onCapacityChange })
      const slider = screen.getByRole('slider')

      expect(slider.getAttribute('aria-disabled')).toBe('true')
      expect(slider.getAttribute('tabindex')).toBe('-1')
      expect(getComputedStyle(slider).color).toBe('var(--muted)')
      expect(getComputedStyle(slider).getPropertyValue(
        '--pipeline-channel-handle-state-color',
      )).toBe('var(--muted)')

      fireEvent.pointerDown(slider, {
        pointerId: 3,
        button: 0,
        clientY: 300,
      })
      fireEvent.pointerMove(slider, { pointerId: 3, clientY: 260 })
      fireEvent.pointerUp(slider, { pointerId: 3, clientY: 260 })
      fireEvent.keyDown(slider, { key: 'ArrowUp' })

      expect(onCapacityChange).not.toHaveBeenCalled()
      view.unmount()
    }
    adapter.dispose()
  })

  it('lifts a nonzero unavailable fixed capacity without enabling commands', () => {
    const adapter = new SimulationAdapter()
    const channel = derivedSnapshot(adapter).readerChannel
    const onCapacityChange = vi.fn()
    const fixedChannel = {
      ...channel,
      capacity: {
        ...channel.capacity,
        applied: 2,
        min: 0,
        max: 2,
        applyMode: 'unavailable' as const,
      },
    }
    const { container } = renderCable(fixedChannel, { onCapacityChange })
    const slider = screen.getByRole('slider')
    const cable = container.querySelector('.pipeline-channel-cable')!

    expect(pathApexY(cable.getAttribute('d')!)).toBeLessThan(
      CHANNEL_CABLE_ENDPOINTS[fixedChannel.id].start.y,
    )
    expect(slider.getAttribute('aria-disabled')).toBe('true')
    fireEvent.keyDown(slider, { key: 'ArrowUp' })
    expect(onCapacityChange).not.toHaveBeenCalled()
    adapter.dispose()
  })

  it('shows a local pointer preview and commits the released capacity', () => {
    const adapter = new SimulationAdapter()
    const channel = channelSnapshot(derivedSnapshot(adapter).readerChannel, 4)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(channel, { onCapacityChange })
    installSvgCoordinates(container.querySelector('svg')!)
    const slider = screen.getByRole('slider')
    const solidPath = container.querySelector('.pipeline-channel-cable')!
    const appliedPath = solidPath.getAttribute('d')

    fireEvent.pointerDown(slider, {
      pointerId: 7,
      button: 0,
      clientY: 300,
    })
    fireEvent.pointerMove(slider, { pointerId: 7, clientY: 260 })

    const ghost = container.querySelector('.pipeline-channel-requested-cable')!
    const handleBody = slider.querySelector('.pipeline-channel-handle__body')!
    const handleValue = slider.querySelector('.pipeline-channel-handle__value')!
    const requestRing = slider.querySelector(
      '.pipeline-channel-handle__request-ring',
    )!
    const appliedStyle = getComputedStyle(solidPath)
    const ghostStyle = getComputedStyle(ghost)
    const handleStyle = getComputedStyle(slider)
    expect(ghost).not.toBeNull()
    expect(ghost.classList).toContain('pipeline-channel-requested-cable--preview')
    expect(ghostStyle.stroke).toBe('var(--cyan)')
    expect(ghostStyle.strokeWidth).toBe('2px')
    expect(ghostStyle.strokeDasharray).toBe('none')
    expect(ghostStyle.opacity).toBe('0.92')
    expect(Number.parseFloat(ghostStyle.strokeWidth)).toBeLessThan(
      Number.parseFloat(appliedStyle.strokeWidth),
    )
    expect(handleStyle.getPropertyValue(
      '--pipeline-channel-handle-state-color',
    )).toBe('var(--cyan)')
    expect(getComputedStyle(handleBody).stroke).toBe(
      'var(--pipeline-channel-handle-state-color)',
    )
    expect(getComputedStyle(handleValue).fill).toBe(
      'var(--pipeline-channel-handle-state-color)',
    )
    expect(getComputedStyle(requestRing).stroke).toBe('var(--cyan)')
    expect(getComputedStyle(requestRing).opacity).toBe('1')
    expect(translatedY(slider)).toBe(pathApexY(ghost.getAttribute('d')!))
    expect(solidPath.getAttribute('d')).toBe(appliedPath)
    expect(slider.getAttribute('aria-valuenow')).toBe('6')

    fireEvent.pointerUp(slider, { pointerId: 7, clientY: 260 })
    expect(onCapacityChange).toHaveBeenCalledWith(channel.id, 6)
    expect(container.querySelector('.pipeline-channel-requested-cable')).toBeNull()
    adapter.dispose()
  })

  it('commits exactly once after release outside the moving handle', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      {
        channel: channelSnapshot(snapshot.readerChannel, 5),
        pointerId: 11,
        releaseY: 240,
        expected: 8,
      },
      {
        channel: channelSnapshot(snapshot.senderChannel, 30),
        pointerId: 12,
        releaseY: 285,
        expected: 40,
      },
    ] as const

    for (const testCase of cases) {
      const onCapacityChange = vi.fn()
      const view = renderCable(testCase.channel, { onCapacityChange })
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
      expect(slider.classList).toContain('pipeline-channel-handle--preview')

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
        testCase.channel.id,
        testCase.expected,
      )
      expect(slider.classList).not.toContain('pipeline-channel-handle--preview')
      view.unmount()
    }
    adapter.dispose()
  })

  it('cancels on lost capture, pointercancel, and Escape', () => {
    const adapter = new SimulationAdapter()
    const channel = channelSnapshot(derivedSnapshot(adapter).senderChannel, 100, 30)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(channel, { onCapacityChange })
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
      expect(slider.classList).toContain('pipeline-channel-handle--preview')

      if (terminalEvent === 'lostcapture') {
        fireEvent.lostPointerCapture(slider, { pointerId: 13 })
      } else if (terminalEvent === 'pointercancel') {
        fireEvent.pointerCancel(document.body, { pointerId: 13 })
      } else {
        fireEvent.keyDown(window, { key: 'Escape' })
      }

      fireEvent.pointerUp(document.body, { pointerId: 13, clientY: 285 })
      expect(onCapacityChange).not.toHaveBeenCalled()
      expect(slider.classList).not.toContain('pipeline-channel-handle--preview')
      expect(slider.getAttribute('aria-valuenow')).toBe('30')
    }
    adapter.dispose()
  })

  it('removes active drag listeners on unmount', () => {
    const adapter = new SimulationAdapter()
    const channel = channelSnapshot(derivedSnapshot(adapter).senderChannel, 100, 30)
    const onCapacityChange = vi.fn()
    const view = renderCable(channel, { onCapacityChange })
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
    const channel = channelSnapshot(derivedSnapshot(adapter).readerChannel, 4)
    const onCapacityChange = vi.fn()
    const { container } = renderCable(channel, { onCapacityChange })
    installSvgCoordinates(container.querySelector('svg')!)
    const slider = screen.getByRole('slider')

    fireEvent.pointerDown(slider, {
      pointerId: 9,
      button: 0,
      clientY: 300,
    })
    fireEvent.pointerMove(slider, { pointerId: 9, clientY: 260 })
    expect(container.querySelector('.pipeline-channel-requested-cable')).not.toBeNull()

    fireEvent.pointerCancel(slider, { pointerId: 9 })
    expect(onCapacityChange).not.toHaveBeenCalled()
    expect(container.querySelector('.pipeline-channel-requested-cable')).toBeNull()
    expect(slider.getAttribute('aria-valuenow')).toBe('4')
    adapter.dispose()
  })

  it('keeps one cable geometry and label z-order stable for pending values', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const cases = [
      { channel: snapshot.readerChannel, applied: 12, candidate: 5 },
      { channel: snapshot.senderChannel, applied: 100, candidate: 30 },
    ] as const

    for (const testCase of cases) {
      const snapshot = channelSnapshot(
        testCase.channel,
        testCase.applied,
        testCase.candidate,
      )
      const endpoints = CHANNEL_CABLE_ENDPOINTS[snapshot.id]
      const appliedY = capacityToCableY(
        testCase.applied,
        snapshot.capacity,
        endpoints.start.y,
        CHANNEL_CABLE_MAX_LIFT,
      )
      const candidateY = capacityToCableY(
        testCase.candidate,
        snapshot.capacity,
        endpoints.start.y,
        CHANNEL_CABLE_MAX_LIFT,
      )
      const candidatePath = buildChannelCablePath(
        endpoints.start,
        endpoints.end,
        candidateY,
      )
      const view = renderCable(snapshot)
      const channelGroup = view.container.querySelector(`#channel-${snapshot.id}`)!
      const cable = channelGroup.querySelector('.pipeline-channel-cable')!
      const appliedLabel = channelGroup.querySelector('.pipeline-channel-capacity-applied')!
      const metricLabels = [...channelGroup.querySelectorAll('.pipeline-channel-metric')]
      const slider = channelGroup.querySelector('.pipeline-channel-handle')!
      const handleBody = slider.querySelector('.pipeline-channel-handle__body')!
      const handleValue = slider.querySelector('.pipeline-channel-handle__value')!
      const children = [...channelGroup.children]
      const cableStyle = getComputedStyle(cable)
      const handleStyle = getComputedStyle(slider)

      expect(cable.getAttribute('d')).toBe(candidatePath)
      expect(channelGroup.querySelector('.pipeline-channel-requested-cable')).toBeNull()
      expect(Number.parseFloat(cableStyle.strokeWidth)).toBeGreaterThan(0)
      expect(handleStyle.getPropertyValue(
        '--pipeline-channel-handle-state-color',
      )).toBe('var(--yellow)')
      expect(getComputedStyle(handleBody).stroke).toBe(
        'var(--pipeline-channel-handle-state-color)',
      )
      expect(getComputedStyle(handleValue).fill).toBe(
        'var(--pipeline-channel-handle-state-color)',
      )
      expect(translatedY(slider)).toBe(candidateY)
      expect(channelGroup.querySelector('.pipeline-channel-markers')).toBeNull()
      expect(channelGroup.querySelector('.pipeline-marker')).toBeNull()
      expect(appliedLabel.getAttribute('transform')).toBe(
        `translate(${(endpoints.start.x + endpoints.end.x) / 2 - 50} ${appliedY})`,
      )
      expect(appliedLabel.textContent).toContain(`Applied ${testCase.applied}`)
      expect(slider.getAttribute('transform')).toBe(
        `translate(${(endpoints.start.x + endpoints.end.x) / 2} ${candidateY})`,
      )
      expect(children.indexOf(cable)).toBeLessThan(children.indexOf(appliedLabel))
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
    const base = derivedSnapshot(adapter).readerChannel
    const pending = channelSnapshot(base, 10, 4)
    const applied = channelSnapshot(base, 4)
    const endpoints = CHANNEL_CABLE_ENDPOINTS[base.id]
    const view = renderCable(pending)

    expect(view.container.querySelector('.pipeline-channel-requested-cable')).toBeNull()
    expect(screen.getByText('Applied 10')).not.toBeNull()
    expect(screen.getByText('Pending 4 batches')).not.toBeNull()

    view.rerender(
      <svg viewBox="0 0 1100 640">
        <ChannelCable
          snapshot={applied}
          start={endpoints.start}
          end={endpoints.end}
          selected={false}
          onSelect={() => undefined}
          onCapacityChange={() => undefined}
        />
      </svg>,
    )

    expect(view.container.querySelector('.pipeline-channel-requested-cable')).toBeNull()
    expect(screen.queryByText(/Applied 10/)).toBeNull()
    expect(screen.queryByText(/Pending 4 batches/)).toBeNull()
    expect(view.container.querySelector('.pipeline-channel-cable')?.getAttribute('d'))
      .toBe(buildChannelCablePath(endpoints.start, endpoints.end, 335))
    adapter.dispose()
  })

  it('selects the channel by click and Enter', async () => {
    const user = userEvent.setup()
    const adapter = new SimulationAdapter()
    const channel = {
      ...derivedSnapshot(adapter).readerChannel,
      displayedPressure: 0.5,
      flowState: 'near-limit' as const,
    }
    const onSelect = vi.fn()
    renderCable(channel, { onSelect })
    const control = screen.getByRole('button', {
      name: 'Inspect reader to throttler channel',
    })

    expect(control.getAttribute('data-pressure')).toBe('0.50')
    expect(control.getAttribute('style')).toContain(
      '--pipeline-channel-pressure-color: #ffd31f',
    )
    expect(control.querySelector('.pipeline-channel-cable')?.getAttribute('stroke'))
      .toBeNull()
    expect(getComputedStyle(screen.getByRole('slider')).getPropertyValue(
      '--pipeline-channel-handle-state-color',
    )).toBe('currentColor')

    await user.click(control)
    control.focus()
    await user.keyboard('{Enter}')

    expect(onSelect).toHaveBeenNthCalledWith(1, channel.id)
    expect(onSelect).toHaveBeenNthCalledWith(2, channel.id)
    adapter.dispose()
  })

  it('applies stopped and connection-error cable overrides immediately', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter).readerChannel
    const endpoints = CHANNEL_CABLE_ENDPOINTS[base.id]
    const view = renderCable({
      ...base,
      displayedPressure: 1,
      flowState: 'stopped',
    })
    let channelGroup = view.container.querySelector(`#channel-${base.id}`)!

    expect(channelGroup.classList).toContain('pipeline-channel--stopped')
    expect(getComputedStyle(channelGroup).color).toBe('var(--muted)')
    expect(getComputedStyle(channelGroup).transition).toBe('none')

    view.rerender(
      <svg viewBox="0 0 1100 640">
        <ChannelCable
          snapshot={{
            ...base,
            displayedPressure: 0,
            flowState: 'connection-error',
          }}
          start={endpoints.start}
          end={endpoints.end}
          selected={false}
          onSelect={() => undefined}
          onCapacityChange={() => undefined}
        />
      </svg>,
    )
    channelGroup = view.container.querySelector(`#channel-${base.id}`)!
    const slider = channelGroup.querySelector('.pipeline-channel-handle')!
    const handleBody = slider.querySelector('.pipeline-channel-handle__body')!
    expect(channelGroup.classList).toContain('pipeline-channel--connection-error')
    expect(getComputedStyle(channelGroup).color).toBe('var(--red)')
    expect(getComputedStyle(channelGroup).transition).toBe('none')
    expect(getComputedStyle(slider).getPropertyValue(
      '--pipeline-channel-handle-state-color',
    )).toBe('currentColor')
    expect(getComputedStyle(handleBody).strokeDasharray).toBe('6 3')
    adapter.dispose()
  })
})
