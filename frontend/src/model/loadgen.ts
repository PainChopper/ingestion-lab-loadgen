export type ActorId = 'reader' | 'throttler' | 'sender' | 'target'

export type QueueId = 'reader-to-throttler' | 'throttler-to-sender'

export type SelectableId = ActorId | QueueId | 'http'

export type AdapterKind = 'simulation' | 'http'

export type ConnectionState =
  | 'connecting'
  | 'connected'
  | 'disconnected'
  | 'error'

export type RunState = 'idle' | 'running' | 'paused'

export type ApplyMode = 'immediate' | 'next-run' | 'unavailable'

export type ThrottlerInstallationMode = 'installed' | 'bypass'

export type ReaderLimitationReason = 'downstream-backpressure'

export type QueueTrend = 'rising' | 'steady' | 'falling' | 'unknown'

export type QueueFlowState =
  | 'normal'
  | 'near-limit'
  | 'backpressure'
  | 'stopped'
  | 'connection-error'

export interface NumericControlSnapshot {
  readonly applied: number | null
  readonly preview: number | null
  readonly pending: number | null
  readonly min: number
  readonly max: number
  readonly step: number
  readonly unit: string
  readonly applyMode: ApplyMode
}

export interface InstallationModeControlSnapshot {
  readonly applied: ThrottlerInstallationMode | null
  readonly pending: ThrottlerInstallationMode | null
  readonly applyMode: ApplyMode
  readonly writable: boolean
  readonly unavailableReason: string | null
}

export interface ReaderSnapshot {
  readonly id: 'reader'
  readonly workers: NumericControlSnapshot
  readonly readBatchSize: NumericControlSnapshot
  readonly readTps: number | null
  readonly configuredCapacityTps: number | null
  readonly limitationReason: ReaderLimitationReason | null
  readonly rowsRead: number | null
  readonly source: string | null
  readonly state: RunState
}

export interface ThrottlerSnapshot {
  readonly id: 'throttler'
  readonly requestedTps: NumericControlSnapshot
  readonly installationMode: InstallationModeControlSnapshot
  readonly admittedTps: number | null
  readonly limitedMs: number | null
  readonly state: RunState
}

export interface QueueTelemetrySnapshot {
  readonly id: QueueId
  readonly from: ActorId
  readonly to: ActorId
  readonly capacity: NumericControlSnapshot
  readonly enqueuedBatchesTotal: number
  readonly enqueuedTransactionsTotal: number
  readonly dequeuedBatchesTotal: number
  readonly dequeuedTransactionsTotal: number
  readonly depthBatches: number | null
  readonly queuedTransactions: number | null
  readonly handoffBatches: number
  readonly handoffBatchesTotal: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly inputBatchesPerSecond: number
  readonly outputBatchesPerSecond: number
  readonly inputTransactionsPerSecond: number
  readonly outputTransactionsPerSecond: number
  readonly inputTps: number | null
  readonly outputTps: number | null
  readonly throughputTps: number | null
  readonly blockedMs: number | null
  readonly trend: QueueTrend
}

export interface QueueSnapshot extends QueueTelemetrySnapshot {
  readonly displayedPressure: number
  readonly flowState: QueueFlowState
}

export interface SenderSnapshot {
  readonly id: 'sender'
  readonly workers: NumericControlSnapshot
  readonly httpBatchSize: NumericControlSnapshot
  readonly timeoutMs: NumericControlSnapshot
  readonly attemptedTps: number | null
  readonly inFlightRequests: number | null
  readonly successfulResponses: number | null
  readonly failedResponses: number | null
  readonly retries: number | null
  readonly state: RunState
}

export interface HttpSnapshot {
  readonly id: 'http'
  readonly connectionState: ConnectionState
  readonly statusCode: number | null
  readonly throughputTps: number | null
  readonly inFlightRequests: number | null
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly latencyP95Ms: number | null
}

export interface TargetSnapshot {
  readonly id: 'target'
  readonly endpoint: string | null
  readonly artificialDelayMs: NumericControlSnapshot
  readonly errorRatePercent: NumericControlSnapshot
  readonly acceptedTps: number | null
  readonly failedTps: number | null
  readonly latencyP95Ms: number | null
  readonly http200Responses: number | null
  readonly http503Responses: number | null
  readonly connectionState: ConnectionState
}

export interface LoadgenTelemetrySnapshot {
  readonly revision: number
  readonly adapterKind: AdapterKind
  readonly connectionState: ConnectionState
  readonly runState: RunState
  readonly elapsedMs: number
  readonly totalTransactions: number
  readonly reader: ReaderSnapshot
  readonly throttler: ThrottlerSnapshot
  readonly queue1: QueueTelemetrySnapshot
  readonly queue2: QueueTelemetrySnapshot
  readonly sender: SenderSnapshot
  readonly http: HttpSnapshot
  readonly target: TargetSnapshot
}

export interface LoadgenSnapshot extends Omit<
  LoadgenTelemetrySnapshot,
  'queue1' | 'queue2'
> {
  readonly queue1: QueueSnapshot
  readonly queue2: QueueSnapshot
}

export type LoadgenCommand =
  | { type: 'run' }
  | { type: 'pause' }
  | { type: 'reset' }
  | { type: 'set-requested-tps'; value: number }
  | {
      type: 'set-throttler-installation-mode'
      value: ThrottlerInstallationMode
    }
  | { type: 'set-worker-count'; actor: 'reader' | 'sender'; value: number }
  | { type: 'set-queue-capacity'; queue: QueueId; value: number }
  | { type: 'set-read-batch-size'; value: number }
  | { type: 'set-http-batch-size'; value: number }
  | { type: 'set-http-timeout'; valueMs: number }
  | { type: 'set-target-delay'; valueMs: number }
  | { type: 'set-target-error-rate'; valuePercent: number }

export interface AdapterError {
  readonly code: 'invalid-command' | 'disposed' | 'unavailable' | 'internal'
  readonly message: string
  readonly retryable: boolean
  readonly details: Readonly<Record<string, unknown>> | null
}

export interface CommandReceipt {
  readonly commandId: string
  readonly commandType: LoadgenCommand['type']
  readonly accepted: boolean
  readonly applyMode: ApplyMode
  readonly appliedAtMs: number | null
  readonly snapshotRevision: number
  readonly error: AdapterError | null
}
