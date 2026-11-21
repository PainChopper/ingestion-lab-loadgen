export type ActorId = 'reader' | 'throttler' | 'sender'

export type ChannelId = 'reader-to-throttler' | 'throttler-to-sender'

export type SelectableId = ActorId | ChannelId

export type AdapterKind = 'simulation' | 'http'

export type ConnectionState = 'connecting' | 'connected' | 'error'

export type RunState = 'starting' | 'running' | 'stopping' | 'faulted'

export type ApplyMode = 'immediate' | 'unavailable'

export type ChannelFlowState =
  'normal' | 'near-limit' | 'backpressure' | 'stopped' | 'connection-error'

export interface NumericControlSnapshot {
  readonly applied: number | null
  readonly min: number
  readonly max: number
  readonly unit: string
  readonly applyMode: ApplyMode
}

export interface InstalledControlSnapshot {
  readonly applied: boolean | null
  readonly applyMode: ApplyMode
}

export type ReaderActivity = 'idle' | 'reading' | 'blocked'
export type SenderActivity = 'idle' | 'inFlight' | 'backoff'

export interface ReaderWorkerState {
  readonly activity: ReaderActivity
  readonly draining: boolean
}

export interface SenderWorkerState {
  readonly activity: SenderActivity
  readonly draining: boolean
}

export interface ReaderSnapshot {
  readonly id: 'reader'
  readonly workers: NumericControlSnapshot
  readonly workerStates: readonly ReaderWorkerState[]
  readonly readBatchSize: number | null
  readonly readTps: number | null
  readonly rowsRead: number | null
  readonly sourcePath?: string
  readonly sourceError: ReaderSourceErrorSnapshot | null
}

export interface ThrottlerSnapshot {
  readonly id: 'throttler'
  readonly requestedTps: NumericControlSnapshot
  readonly installed: InstalledControlSnapshot
  readonly admittedTps: number | null
}

export type ChannelCapacitySnapshot = number | null

export interface ChannelTelemetrySnapshot {
  readonly id: ChannelId
  readonly from: ActorId
  readonly to: ActorId
  readonly capacity: ChannelCapacitySnapshot
  readonly depthBatches: number | null
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly inputTransactionsPerSecond: number | null
  readonly outputTransactionsPerSecond: number | null
}

export interface ChannelSnapshot extends ChannelTelemetrySnapshot {
  readonly displayedPressure: number
  readonly flowState: ChannelFlowState
}

export interface SenderSnapshot {
  readonly id: 'sender'
  readonly workers: NumericControlSnapshot
  readonly workerStates: readonly SenderWorkerState[]
}

export interface ReaderSourceErrorSnapshot {
  readonly operation: string
  readonly path: string
  readonly message: string
}

export interface LoadgenTelemetrySnapshot {
  readonly revision: number
  readonly adapterKind: AdapterKind
  readonly connectionState: ConnectionState
  readonly runState: RunState | null
  readonly elapsedMs: number
  readonly totalTransactions: number
  readonly reader: ReaderSnapshot
  readonly throttler: ThrottlerSnapshot
  readonly readerChannel: ChannelTelemetrySnapshot
  readonly senderChannel: ChannelTelemetrySnapshot
  readonly sender: SenderSnapshot
}

export interface LoadgenSnapshot extends Omit<
  LoadgenTelemetrySnapshot,
  'readerChannel' | 'senderChannel'
> {
  readonly readerChannel: ChannelSnapshot
  readonly senderChannel: ChannelSnapshot
}

export type LoadgenCommand =
  | { type: 'set-requested-tps'; value: number }
  | {
      type: 'set-throttler-installed'
      value: boolean
    }
  | { type: 'set-reader-workers'; value: number }
  | { type: 'set-sender-workers'; value: number }

export interface AdapterError {
  readonly code: 'invalid-command' | 'disposed' | 'unavailable'
  readonly message: string
}

export interface CommandReceipt {
  readonly accepted: boolean
  readonly error: AdapterError | null
}
