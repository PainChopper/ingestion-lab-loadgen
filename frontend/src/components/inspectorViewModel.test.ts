import { describe, expect, it } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenSnapshot, SelectableId } from '../model/loadgen'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'
import {
  formatStateLabel,
  getInspectorViewModel,
} from './inspectorViewModel'

function derivedSnapshot(adapter: SimulationAdapter): LoadgenSnapshot {
  return new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0)
}

function withChannelCapacity(
  snapshot: LoadgenSnapshot,
  applied: number,
  depthBatches: number,
  pending: number | null = null,
): LoadgenSnapshot {
  return {
    ...snapshot,
    readerChannel: {
      ...snapshot.readerChannel,
      depthBatches,
      capacity: {
        ...snapshot.readerChannel.capacity,
        applied,
        preview: pending,
        pending,
      },
    },
  }
}

function withChannelBlocking(
  snapshot: LoadgenSnapshot,
  blockedSenders: number,
  oldestBlockedSenderMs: number,
  blockedMs: number,
): LoadgenSnapshot {
  return {
    ...snapshot,
    readerChannel: {
      ...snapshot.readerChannel,
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
    expect(model?.rows.find((row) => row.label === 'Retry attempts')?.value)
      .toBe('0')
    expect(model?.rows.find((row) => row.label === 'Worker states')?.value)
      .toBe('0 idle · 0 in-flight · 0 backoff')
    expect(model?.rows.find((row) => row.label === 'Workers desired / live / draining')?.value)
      .toBe('3 / 0 / 0')
    expect(model?.rows.find((row) => row.label === 'Retry policy')?.value)
      .toBe('3 attempts · 250/500 ms · ±20% deterministic jitter')
    adapter.dispose()
  })

  it('separates retry attempts, rejections, terminal work, and ambiguity', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter)
    const model = getInspectorViewModel({
      ...base,
      sender: {
        ...base.sender,
        workers: {
          ...base.sender.workers,
          applied: 32,
          pending: 8,
        },
        liveWorkers: 32,
        drainingWorkers: 8,
        workerSlots: Array.from({ length: 32 }, (_, ordinal) => ({
          id: `sender-worker-${ordinal}`,
          ordinal,
          activity: ordinal < 2 ? 'idle' as const : ordinal < 13 ? 'in-flight' as const : 'backoff' as const,
          lifecycle: ordinal < 24 ? 'active' as const : 'draining' as const,
          terminalError: false,
        })),
        attemptedTps: 80_000,
        retryAttemptedTps: 30_000,
        terminalFailedTps: 10_000,
        attemptsStartedTotal: 90,
        retryAttemptsStartedTotal: 60,
        successfulResponses: 10,
        failedResponses: 70,
        timeoutsTotal: 10,
        terminalFailedBatchesTotal: 20,
        terminalFailedTransactionsTotal: 20_000,
        ambiguousTimeoutTransactionsTotal: 10_000,
        duplicateRiskTransactionsTotal: 8_000,
        ambiguousTerminalTransactionsTotal: 4_000,
      },
    }, 'sender')

    expect(model?.rows).toEqual(expect.arrayContaining([
      { label: 'Workers desired / live / draining', value: '32 / 32 / 8' },
      { label: 'Worker states', value: '2 idle · 11 in-flight · 19 backoff' },
      { label: 'Attempted TPS', value: '80,000 tx/s' },
      { label: 'Retry TPS', value: '30,000 tx/s' },
      { label: 'Terminal failed TPS', value: '10,000 tx/s' },
      { label: 'Attempts', value: '90' },
      { label: 'Retry attempts', value: '60' },
      { label: 'Rejected responses', value: '70' },
      { label: 'Timeouts', value: '10' },
      { label: 'Terminal failed transactions', value: '20,000' },
      { label: 'Duplicate-risk transactions', value: '8,000' },
      { label: 'Ambiguous terminal transactions', value: '4,000' },
    ]))
    adapter.dispose()
  })

  it('shows timeout without inventing an HTTP status or target rejection', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter)
    const timeoutSnapshot: LoadgenSnapshot = {
      ...base,
      http: {
        ...base.http,
        statusCode: null,
        lastOutcome: 'timeout',
        requestsFailedTotal: 3,
        requestsTimedOutTotal: 3,
      },
      target: {
        ...base.target,
        rejectedTps: 0,
        http503Responses: 0,
      },
    }

    expect(
      getInspectorViewModel(timeoutSnapshot, 'http')?.rows.find(
        (row) => row.label === 'Status',
      )?.value,
    ).toBe('TIMEOUT')
    expect(getInspectorViewModel(timeoutSnapshot, 'target')?.rows)
      .toEqual(expect.arrayContaining([
        { label: 'Rejected TPS', value: '0 tx/s' },
        { label: 'HTTP 503', value: '0' },
      ]))
    adapter.dispose()
  })

  it('separates reader actual rate, configured capacity, and limitation', () => {
    const adapter = new SimulationAdapter()
    const base = derivedSnapshot(adapter)
    const model = getInspectorViewModel({
      ...base,
      reader: {
        ...base.reader,
        readTps: 50_000,
        configuredCapacityTps: 350_000,
        limitationReason: 'downstream-backpressure',
        rowsRead: 14_000,
        source: 'MBD-mini/trx/part/input.parquet',
      },
    }, 'reader')

    expect(model?.rows).toEqual(expect.arrayContaining([
      { label: 'Actual Read TPS', value: '50,000 tx/s' },
      { label: 'Configured capacity', value: '350,000 tx/s' },
      { label: 'Capacity state', value: 'Downstream limited' },
      { label: 'Rows read', value: '14,000' },
      { label: 'Source', value: 'MBD-mini/trx/part/input.parquet' },
    ]))
    adapter.dispose()
  })

  it('formats channel depth, capacity, state, and rates from the snapshot', () => {
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
      const snapshot = withChannelBlocking(
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

  it('projects immediate rendezvous pressure without inventing channel depth', () => {
    const adapter = new SimulationAdapter()
    const base = adapter.getSnapshot()
    const snapshot = new ChannelFlowStateDeriver().derive({
      ...base,
      runState: 'running',
      readerChannel: {
        ...base.readerChannel,
        depthBatches: 0,
        blockedSenders: 1,
        oldestBlockedSenderMs: 10,
        capacity: {
          ...base.readerChannel.capacity,
          applied: 0,
          preview: 12,
          pending: 12,
        },
      },
    }, 0)
    const model = getInspectorViewModel(snapshot, 'reader-to-throttler')

    expect(model?.rows).toEqual(expect.arrayContaining([
      { label: 'Depth / capacity', value: '0 / 0 batches' },
      { label: 'Pressure', value: '100%' },
      { label: 'Waiting upstream now', value: '1' },
      { label: 'Oldest current wait', value: '10 ms' },
      { label: 'Flow state', value: 'Backpressure' },
    ]))
    adapter.dispose()
  })

  it('keeps channel depth and capacity truthful across apply states', () => {
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
      const snapshot = withChannelCapacity(
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
