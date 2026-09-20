export type ActorId = 'reader' | 'throttler' | 'sender' | 'target'

export type ChannelId = 'reader-to-throttler' | 'throttler-to-sender'

export type SelectableId = ActorId | ChannelId | 'http'

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

export type SenderWorkerState = 'idle' | 'in-flight' | 'backoff'

export interface SenderWorkerStateCounts {
  readonly idle: number
  readonly inFlight: number
  readonly backoff: number
}

export interface SenderWorkerSlotSnapshot {
  readonly id: string
  readonly ordinal: number
  readonly state: SenderWorkerState
}

export interface RetryPolicySnapshot {
  readonly maxAttempts: number
  readonly backoffBaseMs: number
  readonly backoffMultiplier: number
  readonly jitterPercent: number
  readonly retryableStatusCodes: readonly number[]
  readonly retryTimeouts: boolean
}

export type HttpLastOutcome =
  | 'http-response'
  | 'timeout'
  | 'network-error'
  | null

export type ChannelTrend = 'rising' | 'steady' | 'falling' | 'unknown'

export type ChannelFlowState =
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

export interface RangeControlPolicySnapshot {
  readonly default: number
  readonly min: number
  readonly max: number
  readonly step: number
  readonly unit: string
  readonly mutability: string
}

export interface AllowedControlPolicySnapshot {
  readonly default: number
  readonly allowed: readonly number[]
  readonly unit: string
  readonly mutability: string
}

export interface LoadgenPolicySnapshot {
  readonly readerReadBatchSize: RangeControlPolicySnapshot
  readonly readerChannelCapacity: AllowedControlPolicySnapshot
  readonly senderChannelCapacity: AllowedControlPolicySnapshot
  readonly throttlerRequestedTps: RangeControlPolicySnapshot
  readonly throttlerInstallationMode: InstallationModePolicySnapshot
}

export interface InstallationModePolicySnapshot {
  readonly default: ThrottlerInstallationMode
  readonly allowed: readonly ThrottlerInstallationMode[]
  readonly mutability: string
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

export interface ChannelTelemetrySnapshot {
  readonly id: ChannelId
  readonly from: ActorId
  readonly to: ActorId
  readonly capacity: NumericControlSnapshot
  readonly sentBatchesTotal: number
  readonly sentTransactionsTotal: number
  readonly receivedBatchesTotal: number
  readonly receivedTransactionsTotal: number
  readonly depthBatches: number | null
  readonly bufferedTransactions: number | null
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
  readonly trend: ChannelTrend
}

export interface ChannelSnapshot extends ChannelTelemetrySnapshot {
  readonly displayedPressure: number
  readonly flowState: ChannelFlowState
}

export interface SenderSnapshot {
  readonly id: 'sender'
  readonly workers: NumericControlSnapshot
  readonly httpBatchSize: NumericControlSnapshot
  readonly timeoutMs: NumericControlSnapshot
  readonly workerStates: SenderWorkerStateCounts
  readonly workerSlots: readonly SenderWorkerSlotSnapshot[] | null
  readonly retryPolicy: RetryPolicySnapshot | null
  readonly attemptedTps: number | null
  readonly retryAttemptedTps: number | null
  readonly terminalFailedTps: number | null
  readonly inFlightRequests: number | null
  readonly attemptsStartedTotal: number
  readonly retryAttemptsStartedTotal: number
  readonly successfulResponses: number | null
  readonly failedResponses: number | null
  readonly retries: number | null
  readonly timeoutsTotal: number
  readonly terminalFailedBatchesTotal: number
  readonly terminalFailedTransactionsTotal: number
  readonly ambiguousTimeoutTransactionsTotal: number
  readonly duplicateRiskTransactionsTotal: number
  readonly ambiguousTerminalTransactionsTotal: number
  readonly state: RunState
}

export interface HttpSnapshot {
  readonly id: 'http'
  readonly connectionState: ConnectionState
  readonly statusCode: number | null
  readonly lastOutcome: HttpLastOutcome
  readonly throughputTps: number | null
  readonly inFlightRequests: number | null
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly requestsTimedOutTotal: number
  readonly networkErrorsTotal: number
  readonly latencyP95Ms: number | null
}

export interface TargetSnapshot {
  readonly id: 'target'
  readonly endpoint: string | null
  readonly artificialDelayMs: NumericControlSnapshot
  readonly errorRatePercent: NumericControlSnapshot
  readonly acceptedTps: number | null
  readonly rejectedTps: number | null
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
  readonly startError: string | null
  readonly totalTransactions: number
  readonly policy: LoadgenPolicySnapshot | null
  readonly reader: ReaderSnapshot
  readonly throttler: ThrottlerSnapshot
  readonly readerChannel: ChannelTelemetrySnapshot
  readonly senderChannel: ChannelTelemetrySnapshot
  readonly sender: SenderSnapshot
  readonly http: HttpSnapshot
  readonly target: TargetSnapshot
}

export interface LoadgenSnapshot extends Omit<
  LoadgenTelemetrySnapshot,
  'readerChannel' | 'senderChannel'
> {
  readonly readerChannel: ChannelSnapshot
  readonly senderChannel: ChannelSnapshot
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
  | { type: 'set-reader-channel-capacity'; value: number }
  | { type: 'set-sender-channel-capacity'; value: number }
  | { type: 'set-read-batch-size'; value: number }
  | { type: 'set-http-batch-size'; value: number }
  | { type: 'set-http-timeout'; valueMs: number }
  | { type: 'set-target-delay'; valueMs: number }
  | { type: 'set-target-error-rate'; valuePercent: number }

export interface AdapterError {
  readonly code:
    | 'invalid-command'
    | 'invalid-state'
    | 'disposed'
    | 'unavailable'
    | 'internal'
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
