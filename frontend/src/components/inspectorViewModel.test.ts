import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenSnapshot, SelectableId } from '../model/loadgen'
import { QueueFlowStateDeriver } from '../model/queueFlowState'
import {
  formatStateLabel,
  getInspectorViewModel,
} from './inspectorViewModel'

function derivedSnapshot(adapter: SimulationAdapter): LoadgenSnapshot {
  return new QueueFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}

function withQueueCapacity(
  snapshot: LoadgenSnapshot,
  applied: number,
  depthBatches: number,
  pending: number | null = null,
): LoadgenSnapshot {
  return {
    ...snapshot,
    queue1: {
      ...snapshot.queue1,
      depthBatches,
      capacity: {
        ...snapshot.queue1.capacity,
        applied,
        preview: pending,
        pending,
      },
    },
  }
}

function withQueueBlocking(
  snapshot: LoadgenSnapshot,
  blockedSenders: number,
  oldestBlockedSenderMs: number,
  blockedMs: number,
): LoadgenSnapshot {
  return {
    ...snapshot,
    queue1: {
      ...snapshot.queue1,
      blockedSenders,
      oldestBlockedSenderMs,
      blockedMs,
    },
  }
}

describe('inspector view model', () => {
  it('builds a distinct model for every selectable pipeline object', () => {
    const adapter = new SimulationAdapter()
    const snapshot = derivedSnapshot(adapter)
    const selections: SelectableId[] = [
      'reader',
      'throttler',
      'sender',
      'target',
      'reader-to-throttler',
      'throttler-to-sender',
      'http',
    ]

    expect(selections.map((id) => getInspectorViewModel(snapshot, id)?.id)).toEqual(
      selections,
    )
    expect(getInspectorViewModel(snapshot, null)).toBeNull()
    adapter.dispose()
  })

  it('shows complete zeroed simulation metrics before the first run', () => {
    const adapter = new SimulationAdapter()
    const model = getInspectorViewModel(derivedSnapshot(adapter), 'sender')

    expect(model?.rows.find((row) => row.label === 'Attempted TPS')?.value).toBe('0 tx/s')
    expect(model?.rows.find((row) => row.label === 'Retries')?.value).toBe('0')
    adapter.dispose()
  })

  it('formats queue depth, capacity, state, and rates from the snapshot', () => {
    const adapter = new SimulationAdapter()
    const model = getInspectorViewModel(
      derivedSnapshot(adapter),
      'reader-to-throttler',
    )

    expect(model?.rows).toContainEqual({
      label: 'Depth / capacity',
      value: '0 / 4 batches',
    })
    expect(model?.rows).toContainEqual({ label: 'Flow state', value: 'Stopped' })
    expect(model?.rows).toContainEqual({ label: 'Pressure', value: '0%' })
    expect(model?.rows).toContainEqual({ label: 'Throughput', value: '0 tx/s' })
    adapter.dispose()
  })

  it('separates historical blocked time from current upstream waiters', () => {
    const adapter = new SimulationAdapter()
    const cases = [
      {
        blockedSenders: 0,
        oldestMs: 0,
        blockedMs: 1_250,
        expected: ['0', '—', '1,250 ms'],
      },
      {
        blockedSenders: 1,
        oldestMs: 450,
        blockedMs: 1_600,
        expected: ['1', '450 ms', '1,600 ms'],
      },
    ] as const

    for (const testCase of cases) {
      const snapshot = withQueueBlocking(
        derivedSnapshot(adapter),
        testCase.blockedSenders,
        testCase.oldestMs,
        testCase.blockedMs,
      )
      const model = getInspectorViewModel(snapshot, 'reader-to-throttler')
      const labels = [
        'Waiting upstream now',
        'Oldest current wait',
        'Accumulated blocked time',
      ]

      expect(labels.map((label) =>
        model?.rows.find((row) => row.label === label)?.value,
      )).toEqual(testCase.expected)
      expect(model?.rows.some((row) => row.label === 'Blocked time')).toBe(false)
    }
    adapter.dispose()
  })

  it('keeps queue depth and capacity truthful across apply states', () => {
    const cases = [
    {
      name: 'applied 4, depth 4, pending 0',
      applied: 4,
      depth: 4,
      pending: 0,
      expectedDepth: '4 / 4 batches',
      expectedChange: 'Pending 0 batches',
    },
    {
      name: 'applied 12, depth 12, pending 4',
      applied: 12,
      depth: 12,
      pending: 4,
      expectedDepth: '12 / 12 batches',
      expectedChange: 'Pending 4 batches',
    },
    {
      name: 'increase from 4 to 12 applied immediately',
      applied: 12,
      depth: 4,
      pending: null,
      expectedDepth: '4 / 12 batches',
      expectedChange: null,
    },
    {
      name: 'zero capacity applied',
      applied: 0,
      depth: 0,
      pending: null,
      expectedDepth: '0 / 0 batches',
      expectedChange: null,
    },
    ]
    const adapter = new SimulationAdapter()

    for (const testCase of cases) {
      const snapshot = withQueueCapacity(
        derivedSnapshot(adapter),
        testCase.applied,
        testCase.depth,
        testCase.pending,
      )
      const model = getInspectorViewModel(snapshot, 'reader-to-throttler')
      const depthRow = model?.rows.find(
        (row) => row.label === 'Depth / capacity',
      )
      const changeRow = model?.rows.find(
        (row) => row.label === 'Capacity change',
      )

      expect(depthRow?.value, testCase.name).toBe(testCase.expectedDepth)
      expect(changeRow?.value ?? null, testCase.name).toBe(
        testCase.expectedChange,
      )
    }
    adapter.dispose()
  })
})

describe('formatStateLabel', () => {
  it('turns model state identifiers into compact labels', () => {
    expect(formatStateLabel('near-limit')).toBe('Near Limit')
    expect(formatStateLabel('connected')).toBe('Connected')
  })
})
