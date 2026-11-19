import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenSnapshot, SelectableId } from '../model/loadgen'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'
import { formatStateLabel, getInspectorViewModel } from './inspectorViewModel'

function derivedSnapshot(adapter: SimulationAdapter): LoadgenSnapshot {
  return new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}

function withChannelCapacity(
  snapshot: LoadgenSnapshot,
  applied: number,
  depthBatches: number,
): LoadgenSnapshot {
  return {
    ...snapshot,
    readerChannel: {
      ...snapshot.readerChannel,
      depthBatches,
      capacity: applied,
    },
  }
}

function withChannelBlocking(
  snapshot: LoadgenSnapshot,
  blockedSenders: number,
  oldestBlockedSenderMs: number,
): LoadgenSnapshot {
  return {
    ...snapshot,
    readerChannel: {
      ...snapshot.readerChannel,
      blockedSenders,
      oldestBlockedSenderMs,
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
      'reader-to-throttler',
      'throttler-to-sender',
    ]

    expect(selections.map((id) => getInspectorViewModel(snapshot, id)?.id)).toEqual(selections)
    expect(getInspectorViewModel(snapshot, null)).toBeNull()
    adapter.dispose()
  })

  it('shows the live idle Sender pool before the first tick', () => {
    const adapter = new SimulationAdapter()
    const model = getInspectorViewModel(derivedSnapshot(adapter), 'sender')

    expect(model?.rows.find((row) => row.label === 'Worker states')?.value).toBe(
      '3 idle · 0 in-flight · 0 backoff',
    )
    expect(
      model?.rows.find((row) => row.label === 'Workers desired / live / draining')?.value,
    ).toBe('3 / 3 / 0')
    expect(model?.rows.some((row) => row.label === 'Retry policy')).toBe(false)
    adapter.dispose()
  })

  it('shows desired, live, draining and worker state counts', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter)
    const model = getInspectorViewModel(
      {
        ...base,
        sender: {
          ...base.sender,
          workers: {
            ...base.sender.workers,
            applied: 32,
          },
          liveWorkers: 32,
          drainingWorkers: 8,
          idleWorkers: 2,
          inFlightWorkers: 11,
          backoffWorkers: 19,
          drainingIdleWorkers: 0,
          drainingInFlightWorkers: 0,
          drainingBackoffWorkers: 8,
        },
      },
      'sender',
    )

    expect(model?.rows).toEqual(
      expect.arrayContaining([
        { label: 'Workers desired / live / draining', value: '32 / 32 / 8' },
        expect.objectContaining({
          label: 'Worker states',
          value: '2 idle · 11 in-flight · 19 backoff',
        }),
      ]),
    )
    adapter.dispose()
  })

  it.each(['simulation', 'http'] as const)('only shows Sender pool rows in %s', (adapterKind) => {
    const adapter = new SimulationAdapter()
    const snapshot: LoadgenSnapshot = { ...derivedSnapshot(adapter), adapterKind }
    const sender = getInspectorViewModel(snapshot, 'sender')
    expect(sender?.rows.map((row) => row.label)).toEqual([
      'Workers desired / live / draining',
      'Worker states',
    ])
    adapter.dispose()
  })

  it('keeps Reader Inspector focused on pool, state segments, rate, and rows', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter)
    const model = getInspectorViewModel(
      {
        ...base,
        reader: {
          ...base.reader,
          readTps: 50_000,
          rowsRead: 14_000,
        },
      },
      'reader',
    )

    expect(model?.rows).toEqual(
      expect.arrayContaining([
        { label: 'Actual Read TPS', value: '50,000 tx/s' },
        { label: 'Rows read', value: '14,000' },
        expect.objectContaining({
          label: 'Worker states',
          value: '5 idle · 0 reading · 0 blocked · 0 errors',
          segments: [
            { value: '5 idle', tone: 'idle' },
            { value: '0 reading', tone: 'in-flight' },
            { value: '0 blocked', tone: 'backoff' },
            { value: '0 errors', tone: 'error' },
          ],
        }),
      ]),
    )
    expect(model?.rows.map((row) => row.label)).not.toEqual(
      expect.arrayContaining([
        'Reader slots',
        'Capacity telemetry',
        'Configured capacity',
        'Capacity state',
        'Source',
        'State',
      ]),
    )
    adapter.dispose()
  })

  it.each([
    { connectionState: 'connecting', rowsRead: null, expected: '— errors' },
    { connectionState: 'error', rowsRead: null, expected: '— errors' },
    { connectionState: 'connected', rowsRead: 0, expected: '0 errors' },
    { connectionState: 'error', rowsRead: 0, expected: '0 errors' },
  ] as const)(
    'shows $expected in the HTTP Reader Inspector in $connectionState with rowsRead=$rowsRead',
    ({ connectionState, rowsRead, expected }) => {
      const adapter = new SimulationAdapter()
      const base = derivedSnapshot(adapter)
      adapter.dispose()
      const model = getInspectorViewModel(
        {
          ...base,
          adapterKind: 'http',
          connectionState,
          reader: { ...base.reader, rowsRead },
        },
        'reader',
      )

      const workerStates = model?.rows.find((row) => row.label === 'Worker states')
      expect(workerStates?.value).toBe(`5 idle · 0 reading · 0 blocked · ${expected}`)
      expect(workerStates?.segments?.at(-1)).toEqual({ value: expected, tone: 'error' })
    },
  )

  it.each(['connected', 'error'] as const)(
    'shows the Reader source error and counts it in Worker states while %s',
    (connectionState) => {
      const adapter = new SimulationAdapter()
      const base = derivedSnapshot(adapter)
      const sourceDirectory = 'C:/dataset'
      const model = getInspectorViewModel(
        {
          ...base,
          adapterKind: 'http',
          connectionState,
          reader: {
            ...base.reader,
            sourceDirectory,
            sourceError: {
              category: 'source',
              operation: 'read',
              relativePath: 'broken/very-long-source.parquet',
              message: 'corrupt parquet',
            },
          },
        },
        'reader',
      )

      expect(model?.rows).toContainEqual({
        label: '',
        key: 'source-error',
        kind: 'source-error',
        value: 'read: C:/dataset/broken/very-long-source.parquet: corrupt parquet',
        layout: 'full-width',
      })
      const workerStates = model?.rows.find((row) => row.label === 'Worker states')
      expect(workerStates?.value).toMatch(/ · 1 error$/)
      expect(workerStates?.segments?.at(-1)).toEqual({ value: '1 error', tone: 'error' })
      expect(model?.rows.map((row) => row.label)).not.toEqual(
        expect.arrayContaining([
          'Source directory',
          'Source error',
          'Source status',
          'Source operation',
          'Source message',
          'Reader worker',
          'Source path',
        ]),
      )
      expect(model?.rows.filter((row) => row.kind === 'source-error')).toHaveLength(1)
      adapter.dispose()
    },
  )

  it('keeps every Channel Inspector to the fixed four rows', () => {
    const adapter = new SimulationAdapter()
    const model = getInspectorViewModel(derivedSnapshot(adapter), 'reader-to-throttler')

    expect(model?.rows).toContainEqual({
      label: 'Depth / capacity',
      value: '0 / 4 batches',
    })
    expect(model?.rows).toEqual([
      { label: 'Throughput', value: '0 tx/s' },
      { label: 'Depth / capacity', value: '0 / 4 batches' },
      { label: 'Waiting upstream', value: '0' },
      { label: 'Oldest wait', value: '—' },
    ])
    adapter.dispose()
  })

  it('keeps current upstream wait values in the fixed Channel rows', () => {
    const adapter = new SimulationAdapter()
    const cases = [
      {
        blockedSenders: 0,
        oldestMs: 0,
        expected: ['0', '—'],
      },
      {
        blockedSenders: 1,
        oldestMs: 450,
        expected: ['1', '450 ms'],
      },
    ] as const

    for (const testCase of cases) {
      const snapshot = withChannelBlocking(
        derivedSnapshot(adapter),
        testCase.blockedSenders,
        testCase.oldestMs,
      )
      const model = getInspectorViewModel(snapshot, 'reader-to-throttler')
      const labels = ['Waiting upstream', 'Oldest wait']

      expect(labels.map((label) => model?.rows.find((row) => row.label === label)?.value)).toEqual(
        testCase.expected,
      )
      expect(model?.rows.some((row) => row.label === 'Accumulated blocked time')).toBe(false)
    }
    adapter.dispose()
  })

  it('keeps rendezvous channel values without adding inspector pressure rows', () => {
    const adapter = new SimulationAdapter()
    const base = adapter.getSnapshot()
    const snapshot = new ChannelFlowStateDeriver().derive(
      {
        ...base,
        runState: 'running',
        readerChannel: {
          ...base.readerChannel,
          depthBatches: 0,
          blockedSenders: 1,
          oldestBlockedSenderMs: 10,
          capacity: 0,
        },
      },
      0,
    )
    const model = getInspectorViewModel(snapshot, 'reader-to-throttler')

    expect(model?.rows).toEqual(
      expect.arrayContaining([
        { label: 'Depth / capacity', value: '0 / 0 batches' },
        { label: 'Waiting upstream', value: '1' },
        { label: 'Oldest wait', value: '10 ms' },
      ]),
    )
    adapter.dispose()
  })

  it('keeps channel depth and capacity truthful without capacity-change rows', () => {
    const cases = [
      {
        name: 'full queue at capacity 4',
        applied: 4,
        depth: 4,
        expectedDepth: '4 / 4 batches',
      },
      {
        name: 'full queue at capacity 12',
        applied: 12,
        depth: 12,
        expectedDepth: '12 / 12 batches',
      },
      {
        name: 'partially filled queue',
        applied: 12,
        depth: 4,
        expectedDepth: '4 / 12 batches',
      },
      {
        name: 'zero capacity applied',
        applied: 0,
        depth: 0,
        expectedDepth: '0 / 0 batches',
      },
    ]
    const adapter = new SimulationAdapter()

    for (const testCase of cases) {
      const snapshot = withChannelCapacity(
        derivedSnapshot(adapter),
        testCase.applied,
        testCase.depth,
      )
      const model = getInspectorViewModel(snapshot, 'reader-to-throttler')
      const depthRow = model?.rows.find((row) => row.label === 'Depth / capacity')
      expect(depthRow?.value, testCase.name).toBe(testCase.expectedDepth)
      expect(
        model?.rows.some((row) => row.label === 'Capacity change'),
        testCase.name,
      ).toBe(false)
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
