/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type { ChannelSnapshot, SelectableId } from '../../model/loadgen'
import { ChannelFlowStateDeriver } from '../../model/channelFlowState'
import { createPipelineGeometry } from './geometry'
import { getChannelCapacityGeometryPresentation } from './channelCableGeometry'
import { ChannelCable } from './ChannelCable'

const channels = createPipelineGeometry({
  orientation: 'landscape',
  readerWorkers: 1,
  senderWorkers: 1,
}).channels

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

function channelSnapshot(channel: ChannelSnapshot, applied: number): ChannelSnapshot {
  return {
    ...channel,
    depthBatches: applied,
    capacity: {
      ...channel.capacity,
      applied,
    },
  }
}

function renderCable(
  snapshot: ChannelSnapshot,
  options: {
    onSelect?: (id: SelectableId) => void
    capacityValues?: readonly number[]
    batchSize?: ChannelSnapshot['capacity']
  } = {},
) {
  return render(
    <svg viewBox="0 0 1100 640">
      <ChannelCable
        snapshot={snapshot}
        geometry={channels[snapshot.id]}
        selected={false}
        onSelect={options.onSelect ?? (() => undefined)}
        capacityValues={options.capacityValues}
        batchSize={
          options.batchSize ?? {
            ...snapshot.capacity,
            applied: snapshot.capacity.min,
          }
        }
      />
    </svg>,
  )
}

describe('ChannelCable mounted behavior', () => {
  it('keeps batch thickness and capacity path independent', () => {
    const adapter = new SimulationAdapter()
    const channel = derivedSnapshot(adapter).readerChannel
    const sourceBatch = adapter.getSnapshot().reader.readBatchSize
    const lowBatch = { ...sourceBatch, applied: sourceBatch.min }
    const highBatch = { ...sourceBatch, applied: sourceBatch.max }
    const lowView = renderCable(channelSnapshot(channel, 1), {
      batchSize: lowBatch,
    })
    const lowCable = lowView.container.querySelector('.pipeline-channel-cable')!
    const lowPath = lowCable.getAttribute('d')
    const lowWidth = Number.parseFloat(getComputedStyle(lowCable).strokeWidth)
    lowView.unmount()

    const thickView = renderCable(channelSnapshot(channel, 1), {
      batchSize: highBatch,
    })
    const thickCable = thickView.container.querySelector('.pipeline-channel-cable')!
    expect(thickCable.getAttribute('d')).toBe(lowPath)
    expect(Number.parseFloat(getComputedStyle(thickCable).strokeWidth)).toBeGreaterThan(lowWidth)
    thickView.unmount()

    const capacityView = renderCable(channelSnapshot(channel, 12), {
      batchSize: lowBatch,
    })
    const capacityCable = capacityView.container.querySelector('.pipeline-channel-cable')!
    expect(capacityCable.getAttribute('d')).not.toBe(lowPath)
    expect(Number.parseFloat(getComputedStyle(capacityCable).strokeWidth)).toBe(lowWidth)
    expect(
      capacityView.container
        .querySelector('#channel-reader-to-throttler')
        ?.getAttribute('data-leg-count'),
    ).toBe('14')
    capacityView.unmount()
    adapter.dispose()
  })

  it('uses uniform rounded capacity turns and a direct path when no leg fits', () => {
    const adapter = new SimulationAdapter()
    const control = {
      ...derivedSnapshot(adapter).readerChannel.capacity,
      applied: derivedSnapshot(adapter).readerChannel.capacity.max,
    }
    const full = getChannelCapacityGeometryPresentation(
      control,
      { x: 150, y: 415 },
      { x: 355, y: 415 },
    )
    const constrained = getChannelCapacityGeometryPresentation(
      control,
      { x: 150, y: 415 },
      { x: 250, y: 415 },
    )

    expect(full.legCount).toBe(14)
    expect(full.cablePath).toContain(' Q')
    expect(full.cablePath).toMatch(/V/)
    expect(constrained.legCount).toBe(0)
    expect(constrained.cablePath).toBe('M150 415 L250 415')
    adapter.dispose()
  })

  it('activates channel cables only from positive rates in running flow states', () => {
    const adapter = new SimulationAdapter()
    const channel = derivedSnapshot(adapter).readerChannel
    const activeStates = ['normal', 'near-limit', 'backpressure'] as const

    for (const flowState of activeStates) {
      const view = renderCable({
        ...channel,
        flowState,
        inputTransactionsPerSecond: 500,
        outputTransactionsPerSecond: 0,
      })
      const group = view.container.querySelector('#channel-reader-to-throttler')!

      expect(group.classList.contains('pipeline-channel--markers-visible')).toBe(true)
      expect(group.classList.contains('pipeline-channel--flow-active')).toBe(true)
      expect(group.getAttribute('data-input-active')).toBe('true')
      expect(group.getAttribute('data-output-active')).toBe('false')
      view.unmount()
    }

    for (const flowState of ['stopped', 'connection-error'] as const) {
      const view = renderCable({
        ...channel,
        flowState,
        inputTransactionsPerSecond: 500,
        outputTransactionsPerSecond: 0,
      })
      const group = view.container.querySelector('#channel-reader-to-throttler')!

      expect(group.classList.contains('pipeline-channel--markers-visible')).toBe(true)
      expect(group.classList.contains('pipeline-channel--flow-active')).toBe(false)
      expect(group.getAttribute('data-input-active')).toBe('true')
      view.unmount()
    }

    const idleView = renderCable({
      ...channel,
      flowState: 'normal',
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 0,
    })
    const idleGroup = idleView.container.querySelector('#channel-reader-to-throttler')!
    expect(idleGroup.classList.contains('pipeline-channel--markers-visible')).toBe(false)
    expect(idleGroup.classList.contains('pipeline-channel--flow-active')).toBe(false)
    idleView.unmount()

    const senderActiveView = renderCable({
      ...channel,
      id: 'throttler-to-sender',
      from: 'throttler',
      to: 'sender',
      flowState: 'backpressure',
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 500,
    })
    const senderActiveGroup = senderActiveView.container.querySelector(
      '#channel-throttler-to-sender',
    )!
    expect(senderActiveGroup.classList.contains('pipeline-channel--markers-visible')).toBe(true)
    expect(senderActiveGroup.classList.contains('pipeline-channel--flow-active')).toBe(true)
    senderActiveView.unmount()
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
    expect(control.getAttribute('style')).toContain('--pipeline-channel-pressure-color: #ffd31f')
    expect(control.querySelector('.pipeline-channel-cable')?.getAttribute('stroke')).toBeNull()
    expect(screen.queryByRole('slider')).toBeNull()
    expect(screen.getByText('Depth 0 / 4 batches')).not.toBeNull()

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
          geometry={channels[base.id]}
          selected={false}
          onSelect={() => undefined}
          batchSize={{ ...base.capacity, applied: base.capacity.min }}
        />
      </svg>,
    )
    channelGroup = view.container.querySelector(`#channel-${base.id}`)!
    expect(channelGroup.classList).toContain('pipeline-channel--connection-error')
    expect(getComputedStyle(channelGroup).color).toBe('var(--red)')
    expect(getComputedStyle(channelGroup).transition).toBe('none')
    const cable = channelGroup.querySelector('.pipeline-channel-cable')!
    expect(getComputedStyle(cable).strokeDasharray).toBe('15 10')
    adapter.dispose()
  })
})
