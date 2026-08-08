import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { SelectableId } from '../model/loadgen'
import {
  formatStateLabel,
  getInspectorViewModel,
} from './inspectorViewModel'

describe('inspector view model', () => {
  it('builds a distinct model for every selectable pipeline object', () => {
    const adapter = new SimulationAdapter()
    const snapshot = adapter.getSnapshot()
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
    const model = getInspectorViewModel(adapter.getSnapshot(), 'sender')

    expect(model?.rows.find((row) => row.label === 'Attempted TPS')?.value).toBe('0 tx/s')
    expect(model?.rows.find((row) => row.label === 'Retries')?.value).toBe('0')
    adapter.dispose()
  })

  it('formats queue depth, capacity, state, and rates from the snapshot', () => {
    const adapter = new SimulationAdapter()
    const model = getInspectorViewModel(
      adapter.getSnapshot(),
      'reader-to-throttler',
    )

    expect(model?.rows).toContainEqual({
      label: 'Depth / capacity',
      value: '0 / 4 batches',
    })
    expect(model?.rows).toContainEqual({ label: 'Flow state', value: 'Stopped' })
    expect(model?.rows).toContainEqual({ label: 'Throughput', value: '0 tx/s' })
    adapter.dispose()
  })
})

describe('formatStateLabel', () => {
  it('turns model state identifiers into compact labels', () => {
    expect(formatStateLabel('near-limit')).toBe('Near Limit')
    expect(formatStateLabel('connected')).toBe('Connected')
  })
})
