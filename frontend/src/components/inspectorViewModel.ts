import type {
  LoadgenSnapshot,
  NumericControlSnapshot,
  QueueSnapshot,
  SelectableId,
} from '../model/loadgen'
import {
  formatInteger,
  formatMilliseconds,
  formatRate,
} from './pipeline/formatters'
import { getQueueCapacityPresentation } from './pipeline/queueCableGeometry'

export interface InspectorRow {
  readonly label: string
  readonly value: string
}

export interface InspectorViewModel {
  readonly id: SelectableId
  readonly title: string
  readonly kind: string
  readonly rows: ReadonlyArray<InspectorRow>
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

function queueViewModel(queue: QueueSnapshot): InspectorViewModel {
  const capacity = getQueueCapacityPresentation(queue.capacity)
  const appliedCapacity = formatControl({
    ...queue.capacity,
    applied: capacity.applied,
  })
  const depth = formatInteger(queue.depthBatches)
  const capacityChange =
    capacity.requestState === null
      ? []
      : [{
          label: 'Capacity change',
          value: `${capacity.requestState === 'pending' ? 'Pending' : 'Preview'} ${formatInteger(capacity.candidate)} ${queue.capacity.unit}`,
        }]

  return {
    id: queue.id,
    title: queue.id === 'reader-to-throttler' ? 'QUEUE 1' : 'QUEUE 2',
    kind: `${formatStateLabel(queue.from)} to ${formatStateLabel(queue.to)}`,
    rows: [
      { label: 'Throughput', value: formatRate(queue.throughputTps) },
      { label: 'Input rate', value: formatRate(queue.inputTps) },
      { label: 'Output rate', value: formatRate(queue.outputTps) },
      {
        label: 'Depth / capacity',
        value: `${depth} / ${appliedCapacity}`,
      },
      {
        label: 'Pressure',
        value: `${Math.round(queue.displayedPressure * 100)}%`,
      },
      ...capacityChange,
      { label: 'Queued tx', value: formatInteger(queue.queuedTransactions) },
      {
        label: 'Waiting upstream now',
        value: formatInteger(queue.blockedSenders),
      },
      {
        label: 'Oldest current wait',
        value:
          queue.blockedSenders > 0
            ? formatMilliseconds(queue.oldestBlockedSenderMs)
            : '—',
      },
      {
        label: 'Accumulated blocked time',
        value: formatMilliseconds(queue.blockedMs),
      },
      { label: 'Trend', value: formatStateLabel(queue.trend) },
      { label: 'Flow state', value: formatStateLabel(queue.flowState) },
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
    case 'sender':
      return {
        id: selectedId,
        title: 'SENDER',
        kind: 'HTTP client',
        rows: [
          { label: 'Attempted TPS', value: formatRate(snapshot.sender.attemptedTps) },
          { label: 'In-flight', value: formatInteger(snapshot.sender.inFlightRequests) },
          { label: 'Successes', value: formatInteger(snapshot.sender.successfulResponses) },
          { label: 'Failures', value: formatInteger(snapshot.sender.failedResponses) },
          { label: 'Retries', value: formatInteger(snapshot.sender.retries) },
        ],
      }
    case 'target':
      return {
        id: selectedId,
        title: 'TARGET',
        kind: snapshot.target.endpoint ?? 'HTTP endpoint',
        rows: [
          { label: 'Accepted TPS', value: formatRate(snapshot.target.acceptedTps) },
          { label: 'p95 latency', value: formatMilliseconds(snapshot.target.latencyP95Ms) },
          { label: 'HTTP 200', value: formatInteger(snapshot.target.http200Responses) },
          { label: 'HTTP 503', value: formatInteger(snapshot.target.http503Responses) },
          { label: 'Connection', value: formatStateLabel(snapshot.target.connectionState) },
        ],
      }
    case 'reader-to-throttler':
      return queueViewModel(snapshot.queue1)
    case 'throttler-to-sender':
      return queueViewModel(snapshot.queue2)
    case 'http':
      return {
        id: selectedId,
        title: 'HTTP',
        kind: 'Sender to target',
        rows: [
          {
            label: 'Status',
            value:
              snapshot.http.statusCode === null
                ? '—'
                : `HTTP ${formatInteger(snapshot.http.statusCode)}`,
          },
          { label: 'Connection', value: formatStateLabel(snapshot.http.connectionState) },
          { label: 'Throughput', value: formatRate(snapshot.http.throughputTps) },
          { label: 'In-flight', value: formatInteger(snapshot.http.inFlightRequests) },
          { label: 'p95 latency', value: formatMilliseconds(snapshot.http.latencyP95Ms) },
        ],
      }
    case null:
      return null
  }
}
