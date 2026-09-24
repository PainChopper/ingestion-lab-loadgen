import type {
  LoadgenSnapshot,
  NumericControlSnapshot,
  ChannelSnapshot,
  SelectableId,
} from '../model/loadgen'
import {
  formatInteger,
  formatMilliseconds,
  formatRate,
} from './pipeline/formatters'
import { getChannelCapacityPresentation } from './pipeline/channelCableGeometry'

export interface InspectorRow {
  readonly label: string
  readonly value: string
  readonly layout?: 'full-width'
  readonly segments?: ReadonlyArray<InspectorRowSegment>
  readonly disclosureLabel?: string
  readonly disclosureValue?: string
}

export interface InspectorRowSegment {
  readonly value: string
  readonly tone: 'idle' | 'in-flight' | 'backoff' | 'error'
}

export interface InspectorSection {
  readonly title: string
  readonly rows: ReadonlyArray<InspectorRow>
}

export interface InspectorViewModel {
  readonly id: SelectableId
  readonly title: string
  readonly kind: string
  readonly rows: ReadonlyArray<InspectorRow>
  readonly sections?: ReadonlyArray<InspectorSection>
}

function formatControl(
  control: NumericControlSnapshot,
  unit = control.unit,
): string {
  const value = formatInteger(control.applied)
  return value === '—' ? value : `${value} ${unit}`
}

function unavailableRow(label = 'Telemetry'): InspectorRow {
  return { label, value: '—' }
}

export function formatStateLabel(value: string): string {
  return value
    .split('-')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function channelViewModel(channel: ChannelSnapshot): InspectorViewModel {
  const capacity = getChannelCapacityPresentation(channel.capacity)
  const appliedCapacity = formatControl({
    ...channel.capacity,
    applied: capacity.applied,
  })
  const depth = formatInteger(channel.depthBatches)
  return {
    id: channel.id,
    title: channel.id === 'reader-to-throttler' ? 'Reader channel' : 'Sender channel',
    kind: `${formatStateLabel(channel.from)} to ${formatStateLabel(channel.to)}`,
    rows: [
      { label: 'Throughput', value: formatRate(channel.throughputTps) },
      {
        label: 'Depth / capacity',
        value: `${depth} / ${appliedCapacity}`,
      },
      {
        label: 'Waiting upstream',
        value: formatInteger(channel.blockedSenders),
      },
      {
        label: 'Oldest wait',
        value:
          channel.blockedSenders > 0
            ? formatMilliseconds(channel.oldestBlockedSenderMs)
            : '—',
      },
    ],
  }
}

export function getInspectorViewModel(
  snapshot: LoadgenSnapshot,
  selectedId: SelectableId | null,
  frozenObserved = false,
): InspectorViewModel | null {
  switch (selectedId) {
    case 'reader':
      {
        const workerSlots = snapshot.reader.workerSlots ?? []
        const idleWorkers = workerSlots.filter((slot) => slot.activity === 'idle').length
        const readingWorkers = workerSlots.filter((slot) => slot.activity === 'reading').length
        const blockedWorkers = workerSlots.filter((slot) => slot.activity === 'blocked').length
      return {
        id: selectedId,
        title: 'READER',
        kind: 'Parquet source',
        rows: [
		  {
			label: `Workers desired / ${frozenObserved ? 'frozen live' : 'live'} / draining`,
			value: `${formatInteger(snapshot.reader.workers.applied)} / ${formatInteger(snapshot.reader.liveWorkers)} / ${formatInteger(snapshot.reader.drainingWorkers)}`,
		  },
          {
            label: 'Worker states',
            value:
              `${formatInteger(idleWorkers)} idle · ` +
              `${formatInteger(readingWorkers)} reading · ` +
              `${formatInteger(blockedWorkers)} blocked · — errors`,
            layout: 'full-width',
            segments: [
              { value: `${formatInteger(idleWorkers)} idle`, tone: 'idle' },
              { value: `${formatInteger(readingWorkers)} reading`, tone: 'in-flight' },
              { value: `${formatInteger(blockedWorkers)} blocked`, tone: 'backoff' },
              { value: '— errors', tone: 'error' },
            ],
          },
          { label: 'Actual Read TPS', value: formatRate(snapshot.reader.readTps) },
          { label: 'Rows read', value: formatInteger(snapshot.reader.rowsRead) },
        ],
      }
      }
    case 'throttler':
      return {
        id: selectedId,
        title: 'THROTTLER',
        kind: 'Rate control',
        rows: [
          { label: 'Admitted TPS', value: formatRate(snapshot.throttler.admittedTps) },
        ],
      }
    case 'sender': {
      const policy = snapshot.policy?.senderRetry ?? snapshot.sender.retryPolicy
      const workerSlots = snapshot.sender.workerSlots ?? []
      const hasWorkerStateTelemetry = snapshot.sender.workerSlots !== null
      const hasSenderTelemetry = snapshot.adapterKind !== 'http'
      const idleWorkers = workerSlots.filter((slot) => slot.activity === 'idle').length
      const inFlightWorkers = workerSlots.filter((slot) => slot.activity === 'in-flight').length
      const backoffWorkers = workerSlots.filter((slot) => slot.activity === 'backoff').length
      const terminalErrorWorkers = workerSlots.filter((slot) => slot.terminalError).length
      const policyValue = policy === null
        ? '—'
        : `${formatInteger(policy.maxAttempts)} attempts · ` +
          `${formatInteger(policy.backoffBaseMs)}/` +
          `${formatInteger(
            policy.backoffBaseMs * policy.backoffMultiplier,
          )} ms · ±${formatInteger(policy.jitterPercent)}% deterministic jitter`
      const poolRows: ReadonlyArray<InspectorRow> = [
        {
          label: `Workers desired / ${frozenObserved ? 'frozen live' : 'live'} / draining`,
          value: `${formatInteger(snapshot.sender.workers.applied)} / ` +
            `${formatInteger(snapshot.sender.liveWorkers)} / ` +
            `${formatInteger(snapshot.sender.drainingWorkers)}`,
        },
        hasWorkerStateTelemetry
          ? {
              label: 'Worker states',
              value:
                `${formatInteger(idleWorkers)} idle · ` +
                `${formatInteger(inFlightWorkers)} in-flight · ` +
                `${formatInteger(backoffWorkers)} backoff · ` +
                `${formatInteger(terminalErrorWorkers)} errors`,
              layout: 'full-width',
              segments: [
                { value: `${formatInteger(idleWorkers)} idle`, tone: 'idle' },
                { value: `${formatInteger(inFlightWorkers)} in-flight`, tone: 'in-flight' },
                { value: `${formatInteger(backoffWorkers)} backoff`, tone: 'backoff' },
                { value: `${formatInteger(terminalErrorWorkers)} errors`, tone: 'error' },
              ],
            }
          : unavailableRow('Worker states'),
      ]
      const deliveryRows: ReadonlyArray<InspectorRow> = hasSenderTelemetry ? [
        { label: 'Attempted TPS', value: formatRate(snapshot.sender.attemptedTps) },
        { label: 'Retry TPS', value: formatRate(snapshot.sender.retryAttemptedTps) },
        {
          label: 'Terminal failed TPS',
          value: formatRate(snapshot.sender.terminalFailedTps),
        },
        { label: 'In-flight', value: formatInteger(snapshot.sender.inFlightRequests) },
        { label: 'Backoff', value: formatInteger(backoffWorkers) },
        {
          label: '2xx responses',
          value: formatInteger(snapshot.sender.successfulResponses),
        },
        {
          label: 'Rejected responses',
          value: formatInteger(snapshot.sender.failedResponses),
        },
        { label: 'Timeouts', value: formatInteger(snapshot.sender.timeoutsTotal) },
      ] : [unavailableRow('Delivery telemetry')]
      const diagnosticsRows: ReadonlyArray<InspectorRow> = hasSenderTelemetry ? [
        { label: 'Attempts', value: formatInteger(snapshot.sender.attemptsStartedTotal) },
        {
          label: 'Retry attempts',
          value: formatInteger(snapshot.sender.retryAttemptsStartedTotal),
        },
        {
          label: 'Terminal failed batches',
          value: formatInteger(snapshot.sender.terminalFailedBatchesTotal),
        },
        {
          label: 'Terminal failed transactions',
          value: formatInteger(snapshot.sender.terminalFailedTransactionsTotal),
        },
        {
          label: 'Ambiguous timeout transactions',
          value: formatInteger(snapshot.sender.ambiguousTimeoutTransactionsTotal),
        },
        {
          label: 'Duplicate-risk transactions',
          value: formatInteger(snapshot.sender.duplicateRiskTransactionsTotal),
        },
        {
          label: 'Ambiguous terminal transactions',
          value: formatInteger(snapshot.sender.ambiguousTerminalTransactionsTotal),
        },
        {
          label: 'Retry policy',
          value: policyValue,
          layout: 'full-width',
        },
        {
          label: 'Diagnostic interpretation',
          value: 'Retries, timeouts, terminal failures, and ambiguous outcomes are counted separately.',
          layout: 'full-width',
        },
      ] : [unavailableRow('Diagnostics telemetry')]
      const sections: ReadonlyArray<InspectorSection> = [
        { title: 'Pool', rows: poolRows },
        { title: 'Delivery', rows: deliveryRows },
        { title: 'Diagnostics', rows: diagnosticsRows },
      ]
      return {
        id: selectedId,
        title: 'SENDER',
        kind: 'HTTP sender',
        rows: [...poolRows, ...deliveryRows, ...diagnosticsRows],
        sections,
      }
    }
    case 'target': {
      if (snapshot.adapterKind === 'http') {
        return {
          id: selectedId,
          title: 'TARGET',
          kind: 'HTTP endpoint',
          rows: [unavailableRow()],
        }
      }
      return {
        id: selectedId,
        title: 'TARGET',
        kind: snapshot.target.endpoint ?? 'HTTP endpoint',
        rows: [
          { label: 'Accepted TPS', value: formatRate(snapshot.target.acceptedTps) },
          { label: 'Rejected TPS', value: formatRate(snapshot.target.rejectedTps) },
          { label: 'p95 latency', value: formatMilliseconds(snapshot.target.latencyP95Ms) },
          { label: 'HTTP 200', value: formatInteger(snapshot.target.http200Responses) },
          { label: 'HTTP 503', value: formatInteger(snapshot.target.http503Responses) },
          { label: 'Connection', value: formatStateLabel(snapshot.target.connectionState) },
        ],
      }
    }
    case 'reader-to-throttler':
      return channelViewModel(snapshot.readerChannel)
    case 'throttler-to-sender':
      return channelViewModel(snapshot.senderChannel)
    case 'http':
      if (snapshot.adapterKind === 'http') {
        return {
          id: selectedId,
          title: 'HTTP',
          kind: 'Sender to target',
          rows: [unavailableRow()],
        }
      }
      return {
        id: selectedId,
        title: 'HTTP',
        kind: 'Sender to target',
        rows: [
          {
            label: 'Status',
            value:
              snapshot.http.lastOutcome === 'timeout'
                ? 'TIMEOUT'
                : snapshot.http.lastOutcome === 'network-error'
                  ? 'NETWORK ERROR'
                  : snapshot.http.statusCode === null
                    ? '—'
                    : `HTTP ${formatInteger(snapshot.http.statusCode)}`,
          },
          { label: 'Connection', value: formatStateLabel(snapshot.http.connectionState) },
          { label: 'Throughput', value: formatRate(snapshot.http.throughputTps) },
          { label: 'In-flight', value: formatInteger(snapshot.http.inFlightRequests) },
          {
            label: 'Attempts',
            value: formatInteger(snapshot.http.requestsStartedTotal),
          },
          {
            label: 'Rejected responses',
            value: formatInteger(
              Math.max(
                0,
                snapshot.http.requestsFailedTotal -
                  snapshot.http.requestsTimedOutTotal -
                  snapshot.http.networkErrorsTotal,
              ),
            ),
          },
          {
            label: 'Timeouts',
            value: formatInteger(snapshot.http.requestsTimedOutTotal),
          },
          { label: 'p95 latency', value: formatMilliseconds(snapshot.http.latencyP95Ms) },
        ],
      }
    case null:
      return null
  }
}
