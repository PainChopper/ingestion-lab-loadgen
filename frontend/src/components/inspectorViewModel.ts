import { workerStateCounts } from './workerStateCounts'
import type {
  LoadgenSnapshot,
  ChannelCapacitySnapshot,
  ChannelSnapshot,
  SelectableId,
} from '../model/loadgen'
import {
  formatInteger,
  formatMilliseconds,
  formatRate,
  formatReaderErrorCount,
} from './pipeline/formatters'

export interface InspectorRow {
  readonly label: string
  readonly value: string
  readonly key?: string
  readonly kind?: 'source-error'
  readonly layout?: 'full-width'
  readonly segments?: ReadonlyArray<InspectorRowSegment>
}

export interface InspectorRowSegment {
  readonly value: string
  readonly tone: 'idle' | 'in-flight' | 'backoff' | 'error'
}

export interface InspectorViewModel {
  readonly id: SelectableId
  readonly title: string
  readonly kind: string
  readonly rows: ReadonlyArray<InspectorRow>
}

function formatControl(control: ChannelCapacitySnapshot): string {
  const value = formatInteger(control)
  return value === '—' ? value : `${value} batches`
}

export function formatStateLabel(value: string): string {
  return value
    .split('-')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function channelViewModel(channel: ChannelSnapshot): InspectorViewModel {
  const appliedCapacity = formatControl(channel.capacity)
  const depth = formatInteger(channel.depthBatches)
  return {
    id: channel.id,
    title: channel.id === 'reader-to-throttler' ? 'Reader channel' : 'Sender channel',
    kind: `${formatStateLabel(channel.from)} to ${formatStateLabel(channel.to)}`,
    rows: [
      { label: 'Throughput', value: formatRate(channel.outputTransactionsPerSecond) },
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
        value: channel.blockedSenders > 0 ? formatMilliseconds(channel.oldestBlockedSenderMs) : '—',
      },
    ],
  }
}

export function getInspectorViewModel(
  snapshot: LoadgenSnapshot,
  selectedId: SelectableId | null,
): InspectorViewModel | null {
  switch (selectedId) {
    case 'reader': {
      const counts = workerStateCounts(snapshot.reader.workerStates)
      const { idleWorkers, readingWorkers, blockedWorkers } = counts
      const sourceError = snapshot.reader.sourceError
      const hasSourceError = sourceError !== null && sourceError !== undefined
      const errorCount = formatReaderErrorCount(snapshot.reader)
      const rows: InspectorRow[] = [
        {
          label: 'Workers desired / live / draining',
          value: `${formatInteger(snapshot.reader.workers.applied)} / ${formatInteger(counts.liveWorkers)} / ${formatInteger(counts.drainingWorkers)}`,
        },
        {
          label: 'Worker states',
          value:
            `${formatInteger(idleWorkers)} idle · ` +
            `${formatInteger(readingWorkers)} reading · ` +
            `${formatInteger(blockedWorkers)} blocked · ${errorCount}`,
          layout: 'full-width',
          segments: [
            { value: `${formatInteger(idleWorkers)} idle`, tone: 'idle' },
            { value: `${formatInteger(readingWorkers)} reading`, tone: 'in-flight' },
            { value: `${formatInteger(blockedWorkers)} blocked`, tone: 'backoff' },
            { value: errorCount, tone: 'error' },
          ],
        },
        { label: 'Actual Read TPS', value: formatRate(snapshot.reader.readTps) },
        { label: 'Rows read', value: formatInteger(snapshot.reader.rowsRead) },
      ]
      if (hasSourceError) {
        const sourcePath = `${snapshot.reader.sourceDirectory ?? ''}/${sourceError.relativePath}`
          .replace(/\/+/g, '/')
          .replace(/\/$/, '')
        rows.push({
          label: '',
          key: 'source-error',
          kind: 'source-error',
          value: `${sourceError.operation}: ${sourcePath}: ${sourceError.message}`,
          layout: 'full-width',
        })
      }
      return {
        id: selectedId,
        title: 'READER',
        kind: 'Parquet source',
        rows,
      }
    }
    case 'throttler':
      return {
        id: selectedId,
        title: 'THROTTLER',
        kind: 'Rate control',
        rows: [{ label: 'Admitted TPS', value: formatRate(snapshot.throttler.admittedTps) }],
      }
    case 'sender': {
      const counts = workerStateCounts(snapshot.sender.workerStates)
      const { idleWorkers, inFlightWorkers, backoffWorkers } = counts
      const sender = { id: selectedId, title: 'SENDER', kind: 'HTTP sender' }
      const poolRows: ReadonlyArray<InspectorRow> = [
        {
          label: 'Workers desired / live / draining',
          value:
            `${formatInteger(snapshot.sender.workers.applied)} / ` +
            `${formatInteger(counts.liveWorkers)} / ` +
            `${formatInteger(counts.drainingWorkers)}`,
        },
        {
          label: 'Worker states',
          value:
            `${formatInteger(idleWorkers)} idle · ` +
            `${formatInteger(inFlightWorkers)} in-flight · ` +
            `${formatInteger(backoffWorkers)} backoff`,
          layout: 'full-width',
          segments: [
            { value: `${formatInteger(idleWorkers)} idle`, tone: 'idle' },
            { value: `${formatInteger(inFlightWorkers)} in-flight`, tone: 'in-flight' },
            { value: `${formatInteger(backoffWorkers)} backoff`, tone: 'backoff' },
          ],
        },
      ]
      return { ...sender, rows: poolRows }
    }
    case 'reader-to-throttler':
      return channelViewModel(snapshot.readerChannel)
    case 'throttler-to-sender':
      return channelViewModel(snapshot.senderChannel)
    case null:
      return null
  }
}
