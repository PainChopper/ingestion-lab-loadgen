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
  const capacityChange =
    capacity.requestState === null
      ? []
      : [{
          label: 'Capacity change',
          value: `${capacity.requestState === 'pending' ? 'Pending' : 'Preview'} ${formatInteger(capacity.candidate)} ${channel.capacity.unit}`,
        }]

  return {
    id: channel.id,
    title: channel.id === 'reader-to-throttler' ? 'Reader channel' : 'Sender channel',
    kind: `${formatStateLabel(channel.from)} to ${formatStateLabel(channel.to)}`,
    rows: [
      { label: 'Throughput', value: formatRate(channel.throughputTps) },
      { label: 'Input rate', value: formatRate(channel.inputTps) },
      { label: 'Output rate', value: formatRate(channel.outputTps) },
      {
        label: 'Depth / capacity',
        value: `${depth} / ${appliedCapacity}`,
      },
      {
        label: 'Pressure',
        value: `${Math.round(channel.displayedPressure * 100)}%`,
      },
      ...capacityChange,
      { label: 'Buffered tx', value: formatInteger(channel.bufferedTransactions) },
      {
        label: 'Waiting upstream now',
        value: formatInteger(channel.blockedSenders),
      },
      {
        label: 'Oldest current wait',
        value:
          channel.blockedSenders > 0
            ? formatMilliseconds(channel.oldestBlockedSenderMs)
            : '—',
      },
      {
        label: 'Accumulated blocked time',
        value: formatMilliseconds(channel.blockedMs),
      },
      { label: 'Trend', value: formatStateLabel(channel.trend) },
      { label: 'Flow state', value: formatStateLabel(channel.flowState) },
    ],
  }
}

export function getInspectorViewModel(
  snapshot: LoadgenSnapshot,
  selectedId: SelectableId | null,
): InspectorViewModel | null {
  switch (selectedId) {
    case 'reader':
      return {
        id: selectedId,
        title: 'READER',
        kind: 'Parquet source',
        rows: [
          { label: 'Actual Read TPS', value: formatRate(snapshot.reader.readTps) },
          {
            label: 'Configured capacity',
            value: formatRate(snapshot.reader.configuredCapacityTps),
          },
          {
            label: 'Capacity state',
            value: snapshot.reader.limitationReason === 'downstream-backpressure'
              ? 'Downstream limited'
              : 'Available',
          },
          { label: 'Rows read', value: formatInteger(snapshot.reader.rowsRead) },
          { label: 'Source', value: snapshot.reader.source ?? '—' },
          { label: 'State', value: formatStateLabel(snapshot.reader.state) },
        ],
      }
    case 'throttler':
      return {
        id: selectedId,
        title: 'THROTTLER',
        kind: 'Rate control',
        rows: [
          {
            label: 'Valve mode',
            value: snapshot.throttler.installationMode.pending === null
              ? formatStateLabel(
                snapshot.throttler.installationMode.applied ?? 'unavailable',
              )
              : `${formatStateLabel(snapshot.throttler.installationMode.applied ?? 'unavailable')} → ${formatStateLabel(snapshot.throttler.installationMode.pending)}`,
          },
          { label: 'Admitted TPS', value: formatRate(snapshot.throttler.admittedTps) },
          { label: 'State', value: formatStateLabel(snapshot.throttler.state) },
          { label: 'Limited time', value: formatMilliseconds(snapshot.throttler.limitedMs) },
        ],
      }
    case 'sender': {
      const policy = snapshot.policy?.senderRetry ?? snapshot.sender.retryPolicy
      const workerSlots = snapshot.sender.workerSlots ?? []
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
          label: 'Workers desired / live / draining',
          value: `${formatInteger(snapshot.sender.workers.applied)} / ` +
            `${formatInteger(snapshot.sender.liveWorkers)} / ` +
            `${formatInteger(snapshot.sender.drainingWorkers)}`,
        },
        {
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
        },
      ]
      const metricsRows: ReadonlyArray<InspectorRow> = [
        { label: 'Attempted TPS', value: formatRate(snapshot.sender.attemptedTps) },
        { label: 'Retry TPS', value: formatRate(snapshot.sender.retryAttemptedTps) },
        {
          label: 'Terminal failed TPS',
          value: formatRate(snapshot.sender.terminalFailedTps),
        },
        { label: 'In-flight', value: formatInteger(snapshot.sender.inFlightRequests) },
        { label: 'Backoff', value: formatInteger(backoffWorkers) },
        { label: 'Attempts', value: formatInteger(snapshot.sender.attemptsStartedTotal) },
        {
          label: 'Retry attempts',
          value: formatInteger(snapshot.sender.retryAttemptsStartedTotal),
        },
        {
          label: '2xx responses',
          value: formatInteger(snapshot.sender.successfulResponses),
        },
        {
          label: 'Rejected responses',
          value: formatInteger(snapshot.sender.failedResponses),
        },
        { label: 'Timeouts', value: formatInteger(snapshot.sender.timeoutsTotal) },
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
      ]
      const diagnosticsRows: ReadonlyArray<InspectorRow> = [
        { label: 'Retry policy', value: policyValue, layout: 'full-width' },
        {
          label: 'Diagnostic interpretation',
          value: 'Retries, timeouts, terminal failures, and ambiguous outcomes are counted separately.',
          layout: 'full-width',
        },
      ]
      const sections: ReadonlyArray<InspectorSection> = [
        { title: 'Состояние pool', rows: poolRows },
        { title: 'Метрики и результаты', rows: metricsRows },
        { title: 'Диагностика и retry policy', rows: diagnosticsRows },
      ]
      return {
        id: selectedId,
        title: 'SENDER',
        kind: 'Simulated sender',
        rows: [...poolRows, ...metricsRows, ...diagnosticsRows],
        sections,
      }
    }
    case 'target': {
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
