import { afterEach, describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenSnapshot, SelectableId } from '../model/loadgen'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'
import { formatStateLabel, getInspectorViewModel } from './inspectorViewModel'

const adapters: SimulationAdapter[] = []
function derivedSnapshot(): LoadgenSnapshot {
  const adapter = new SimulationAdapter()
  adapters.push(adapter)
  return new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}
afterEach(() => adapters.splice(0).forEach((adapter) => adapter.dispose()))

describe('inspector view model', () => {
  it('builds a distinct model for every selectable pipeline object', () => {
    const snapshot = derivedSnapshot()
    const selections: SelectableId[] = [
      'reader',
      'throttler',
      'sender',
      'reader-to-throttler',
      'throttler-to-sender',
    ]
    expect(selections.map((id) => getInspectorViewModel(snapshot, id)?.id)).toEqual(selections)
    expect(getInspectorViewModel(snapshot, null)).toBeNull()
  })

  it('shows desired, live, draining and Sender worker state counts', () => {
    const base = derivedSnapshot()
    const model = getInspectorViewModel(
      {
        ...base,
        sender: {
          ...base.sender,
          workers: { ...base.sender.workers, applied: 24 },
          workerStates: [
            ...Array.from({ length: 2 }, () => ({ activity: 'idle' as const, draining: false })),
            ...Array.from({ length: 11 }, () => ({
              activity: 'inFlight' as const,
              draining: false,
            })),
            ...Array.from({ length: 19 }, (_, index) => ({
              activity: 'backoff' as const,
              draining: index < 8,
            })),
          ],
        },
      },
      'sender',
    )
    expect(model?.rows.map((row) => row.label)).toEqual([
      'Workers desired / live / draining',
      'Worker states',
    ])
    expect(model?.rows[0].value).toBe('24 / 32 / 8')
    expect(model?.rows[1].value).toBe('2 idle · 11 in-flight · 19 backoff')
    expect(model?.rows[1].segments?.map((segment) => segment.tone)).toEqual([
      'idle',
      'in-flight',
      'backoff',
    ])
  })

  it('shows Reader pool, rate, rows and semantic state segments', () => {
    const base = derivedSnapshot()
    const model = getInspectorViewModel(
      {
        ...base,
        reader: { ...base.reader, readTps: 50_000, rowsRead: 14_000 },
      },
      'reader',
    )
    expect(model?.rows).toContainEqual({ label: 'Actual Read TPS', value: '50,000 tx/s' })
    expect(model?.rows).toContainEqual({ label: 'Rows read', value: '14,000' })
    const states = model?.rows.find((row) => row.label === 'Worker states')
    expect(states?.value).toBe('5 idle · 0 reading · 0 blocked · 0 errors')
    expect(states?.segments?.map((segment) => segment.tone)).toEqual([
      'idle',
      'in-flight',
      'backoff',
      'error',
    ])
  })

  it.each([
    { connectionState: 'connecting', rowsRead: null, expected: '— errors' },
    { connectionState: 'error', rowsRead: null, expected: '— errors' },
    { connectionState: 'connected', rowsRead: 0, expected: '0 errors' },
    { connectionState: 'error', rowsRead: 0, expected: '0 errors' },
  ] as const)(
    'shows $expected for HTTP $connectionState with rowsRead=$rowsRead',
    ({ connectionState, rowsRead, expected }) => {
      const base = derivedSnapshot()
      const model = getInspectorViewModel(
        {
          ...base,
          adapterKind: 'http',
          connectionState,
          reader: { ...base.reader, rowsRead },
        },
        'reader',
      )
      expect(model?.rows.find((row) => row.label === 'Worker states')?.segments?.at(-1)).toEqual({
        value: expected,
        tone: 'error',
      })
    },
  )

  it.each(['connected', 'error'] as const)(
    'shows raw Reader operation, full path and message while %s',
    (connectionState) => {
      const base = derivedSnapshot()
      const model = getInspectorViewModel(
        {
          ...base,
          adapterKind: 'http',
          connectionState,
          reader: {
            ...base.reader,
            sourceDirectory: 'C:/dataset',
            sourceError: {
              category: 'source',
              operation: 'future-operation',
              relativePath: 'broken/source.parquet',
              message: 'corrupt parquet',
            },
          },
        },
        'reader',
      )
      const errors = model?.rows.filter((row) => row.kind === 'source-error')
      expect(errors).toHaveLength(1)
      expect(errors?.[0]).toMatchObject({
        value: 'future-operation: C:/dataset/broken/source.parquet: corrupt parquet',
        layout: 'full-width',
      })
      expect(model?.rows.find((row) => row.label === 'Worker states')?.segments?.at(-1)).toEqual({
        value: '1 error',
        tone: 'error',
      })
    },
  )

  it.each([
    { capacity: 4, depth: 4, blocked: 0, oldest: 0, wait: '—' },
    { capacity: 100, depth: 12, blocked: 1, oldest: 450, wait: '450 ms' },
    { capacity: 0, depth: 0, blocked: 1, oldest: 10, wait: '10 ms' },
  ])(
    'shows truthful channel values at capacity=$capacity',
    ({ capacity, depth, blocked, oldest, wait }) => {
      const base = derivedSnapshot()
      const model = getInspectorViewModel(
        {
          ...base,
          readerChannel: {
            ...base.readerChannel,
            capacity,
            depthBatches: depth,
            blockedSenders: blocked,
            oldestBlockedSenderMs: oldest,
            outputTransactionsPerSecond: 1_234,
          },
        },
        'reader-to-throttler',
      )
      const value = (label: string) => model?.rows.find((row) => row.label === label)?.value
      expect(model?.rows).toHaveLength(4)
      expect(value('Throughput')).toBe('1,234 tx/s')
      expect(value('Depth / capacity')).toBe(`${depth} / ${capacity} batches`)
      expect(value('Waiting upstream')).toBe(String(blocked))
      expect(value('Oldest wait')).toBe(wait)
    },
  )
})

describe('formatStateLabel', () => {
  it('turns model state identifiers into compact labels', () => {
    expect(formatStateLabel('near-limit')).toBe('Near Limit')
    expect(formatStateLabel('connected')).toBe('Connected')
  })
})
