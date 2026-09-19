import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  CommandReceipt,
  ConnectionState,
  LoadgenCommand,
  LoadgenPolicySnapshot,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  ChannelTelemetrySnapshot,
  RunState,
  ThrottlerInstallationMode,
} from '../model/loadgen'
const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const COMMAND_ENDPOINT = '/api/loadgen/commands'
const POLL_INTERVAL_MS = 1_000
const UNAVAILABLE_REASON = 'Недоступно в HTTP snapshot mode'
const UNAVAILABLE_COMMAND_MESSAGE = 'command is not available in the HTTP adapter'
const DISPOSED_COMMAND_MESSAGE = 'http adapter is disposed'
const NETWORK_COMMAND_MESSAGE = 'command request failed due to a network error'
const WIRE_KEYS = Object.freeze([
  'elapsedMs',
  'policy',
  'readerChannelBlockedMs',
  'readerChannelBlockedSenders',
  'readerChannelCapacity',
  'readerChannelDepthBatches',
  'readerChannelReceivedBatchesTotal',
  'readerChannelReceivedTransactionsTotal',
  'readerChannelSentBatchesTotal',
  'readerChannelSentTransactionsTotal',
  'readerChannelInputBatchesPerSecond',
  'readerChannelInputTransactionsPerSecond',
  'readerChannelOldestBlockedSenderMs',
  'readerChannelOutputBatchesPerSecond',
  'readerChannelOutputTransactionsPerSecond',
  'readerChannelBufferedTransactions',
  'readerReadBatchSize',
  'readerReadTps',
  'readerRowsRead',
  'readerSource',
  'readerWorkers',
  'runState',
  'senderWorkers',
  'senderChannelBlockedMs',
  'senderChannelBlockedSenders',
  'senderChannelCapacity',
  'senderChannelDepthBatches',
  'senderChannelReceivedBatchesTotal',
  'senderChannelReceivedTransactionsTotal',
  'senderChannelSentBatchesTotal',
  'senderChannelSentTransactionsTotal',
  'senderChannelInputBatchesPerSecond',
  'senderChannelInputTransactionsPerSecond',
  'senderChannelOldestBlockedSenderMs',
  'senderChannelOutputBatchesPerSecond',
  'senderChannelOutputTransactionsPerSecond',
  'senderChannelBufferedTransactions',
  'startError',
  'totalTransactions',
  'throttlerAdmittedTps',
  'throttlerInstallationMode',
  'throttlerRequestedTps',
].sort())

interface WireSnapshot {
  readonly runState: RunState
  readonly elapsedMs: number
  readonly startError: string | null
  readonly totalTransactions: number
  readonly policy: LoadgenPolicySnapshot
  readonly readerWorkers: number
  readonly senderWorkers: number
  readonly readerReadTps: number
  readonly readerReadBatchSize: number
  readonly throttlerRequestedTps: number
  readonly throttlerAdmittedTps: number
  readonly throttlerInstallationMode: ThrottlerInstallationMode
  readonly readerRowsRead: number
  readonly readerSource: string | null
  readonly readerChannelCapacity: number
  readonly readerChannelSentBatchesTotal: number
  readonly readerChannelSentTransactionsTotal: number
  readonly readerChannelReceivedBatchesTotal: number
  readonly readerChannelReceivedTransactionsTotal: number
  readonly readerChannelDepthBatches: number
  readonly readerChannelBufferedTransactions: number
  readonly readerChannelBlockedSenders: number
  readonly readerChannelOldestBlockedSenderMs: number
  readonly readerChannelBlockedMs: number
  readonly readerChannelInputBatchesPerSecond: number
  readonly readerChannelInputTransactionsPerSecond: number
  readonly readerChannelOutputBatchesPerSecond: number
  readonly readerChannelOutputTransactionsPerSecond: number
  readonly senderChannelCapacity: number
  readonly senderChannelSentBatchesTotal: number
  readonly senderChannelSentTransactionsTotal: number
  readonly senderChannelReceivedBatchesTotal: number
  readonly senderChannelReceivedTransactionsTotal: number
  readonly senderChannelDepthBatches: number
  readonly senderChannelBufferedTransactions: number
  readonly senderChannelBlockedSenders: number
  readonly senderChannelOldestBlockedSenderMs: number
  readonly senderChannelBlockedMs: number
  readonly senderChannelInputBatchesPerSecond: number
  readonly senderChannelInputTransactionsPerSecond: number
  readonly senderChannelOutputBatchesPerSecond: number
  readonly senderChannelOutputTransactionsPerSecond: number
}

type SupportedCommand = Extract<
  LoadgenCommand,
  {
    type:
      | 'run'
      | 'pause'
      | 'reset'
      | 'set-reader-channel-capacity'
      | 'set-read-batch-size'
      | 'set-requested-tps'
      | 'set-throttler-installation-mode'
  }
>

function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object' || Object.isFrozen(value)) {
    return value
  }

  for (const key of Reflect.ownKeys(value)) {
    deepFreeze((value as Record<PropertyKey, unknown>)[key])
  }
  return Object.freeze(value)
}

function isSupportedCommand(
  command: LoadgenCommand,
): command is SupportedCommand {
  return command.type === 'run' ||
    command.type === 'pause' ||
    command.type === 'reset' ||
    command.type === 'set-reader-channel-capacity' ||
    command.type === 'set-read-batch-size' ||
    command.type === 'set-requested-tps' ||
    command.type === 'set-throttler-installation-mode'
}

function unavailableControl(unit: string): NumericControlSnapshot {
  return {
    applied: null,
    preview: null,
    pending: null,
    min: 0,
    max: 0,
    step: 1,
    unit,
    applyMode: 'unavailable',
  }
}

function workerControl(value: number | null): NumericControlSnapshot {
  return fixedControl(value, 'workers')
}

function fixedControl(
  value: number | null,
  unit: string,
): NumericControlSnapshot {
  if (value === null) return unavailableControl(unit)

  return {
    applied: value,
    preview: null,
    pending: null,
    min: value,
    max: value,
    step: 1,
    unit,
    applyMode: 'unavailable',
  }
}

function neutralChannel(
  id: ChannelTelemetrySnapshot['id'],
  from: ChannelTelemetrySnapshot['from'],
  to: ChannelTelemetrySnapshot['to'],
): ChannelTelemetrySnapshot {
  return {
    id,
    from,
    to,
    capacity: unavailableControl('batches'),
    sentBatchesTotal: 0,
    sentTransactionsTotal: 0,
    receivedBatchesTotal: 0,
    receivedTransactionsTotal: 0,
    depthBatches: null,
    bufferedTransactions: null,
    handoffBatches: 0,
    handoffBatchesTotal: 0,
    blockedSenders: 0,
    oldestBlockedSenderMs: 0,
    inputBatchesPerSecond: 0,
    outputBatchesPerSecond: 0,
    inputTransactionsPerSecond: 0,
    outputTransactionsPerSecond: 0,
    inputTps: null,
    outputTps: null,
    throughputTps: null,
    blockedMs: null,
    trend: 'unknown',
  }
}

function readerChannelCapacityControl(
  value: number,
  policy: LoadgenPolicySnapshot['readerChannelCapacity'],
  connectionState: ConnectionState,
  runState: RunState,
): NumericControlSnapshot {
  return {
    applied: value,
    preview: null,
    pending: null,
    min: policy.allowed[0]!,
    max: policy.allowed.at(-1)!,
    step: 1,
    unit: policy.unit,
    applyMode: connectionState === 'connected' && runState === 'idle'
      ? 'immediate'
      : 'unavailable',
  }
}

function readerChannel(
  wire: WireSnapshot | null,
  connectionState: ConnectionState,
  runState: RunState,
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'reader-to-throttler',
    'reader',
    'throttler',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: readerChannelCapacityControl(
      wire.readerChannelCapacity,
      wire.policy.readerChannelCapacity,
      connectionState,
      runState,
    ),
    depthBatches: wire.readerChannelDepthBatches,
    bufferedTransactions: wire.readerChannelBufferedTransactions,
    sentBatchesTotal: wire.readerChannelSentBatchesTotal,
    sentTransactionsTotal: wire.readerChannelSentTransactionsTotal,
    receivedBatchesTotal: wire.readerChannelReceivedBatchesTotal,
    receivedTransactionsTotal: wire.readerChannelReceivedTransactionsTotal,
    inputBatchesPerSecond: wire.readerChannelInputBatchesPerSecond,
    inputTransactionsPerSecond: wire.readerChannelInputTransactionsPerSecond,
    outputBatchesPerSecond: wire.readerChannelOutputBatchesPerSecond,
    outputTransactionsPerSecond: wire.readerChannelOutputTransactionsPerSecond,
    inputTps: wire.readerChannelInputTransactionsPerSecond,
    outputTps: wire.readerChannelOutputTransactionsPerSecond,
    throughputTps: wire.readerChannelOutputTransactionsPerSecond,
    blockedSenders: wire.readerChannelBlockedSenders,
    oldestBlockedSenderMs: wire.readerChannelOldestBlockedSenderMs,
    blockedMs: wire.readerChannelBlockedMs,
  }
}

function senderChannel(wire: WireSnapshot | null): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'throttler-to-sender',
    'throttler',
    'sender',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: fixedControl(wire.senderChannelCapacity, 'batches'),
    depthBatches: wire.senderChannelDepthBatches,
    bufferedTransactions: wire.senderChannelBufferedTransactions,
    sentBatchesTotal: wire.senderChannelSentBatchesTotal,
    sentTransactionsTotal: wire.senderChannelSentTransactionsTotal,
    receivedBatchesTotal: wire.senderChannelReceivedBatchesTotal,
    receivedTransactionsTotal: wire.senderChannelReceivedTransactionsTotal,
    inputBatchesPerSecond: wire.senderChannelInputBatchesPerSecond,
    inputTransactionsPerSecond: wire.senderChannelInputTransactionsPerSecond,
    outputBatchesPerSecond: wire.senderChannelOutputBatchesPerSecond,
    outputTransactionsPerSecond: wire.senderChannelOutputTransactionsPerSecond,
    inputTps: wire.senderChannelInputTransactionsPerSecond,
    outputTps: wire.senderChannelOutputTransactionsPerSecond,
    throughputTps: wire.senderChannelOutputTransactionsPerSecond,
    blockedSenders: wire.senderChannelBlockedSenders,
    oldestBlockedSenderMs: wire.senderChannelOldestBlockedSenderMs,
    blockedMs: wire.senderChannelBlockedMs,
  }
}

function createSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: WireSnapshot | null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.runState ?? 'idle'

  return deepFreeze({
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.elapsedMs ?? 0,
    startError: wire?.startError ?? null,
    totalTransactions: wire?.totalTransactions ?? 0,
    policy: wire?.policy ?? null,
    reader: {
      id: 'reader',
      workers: workerControl(wire?.readerWorkers ?? null),
      readBatchSize: readBatchSizeControl(
        wire?.readerReadBatchSize ?? null,
        wire?.policy.readerReadBatchSize ?? null,
        connectionState,
        runState,
      ),
      readTps: wire?.readerReadTps ?? null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: wire?.readerRowsRead ?? null,
      source: wire?.readerSource ?? null,
      state: runState,
    },
    throttler: {
      id: 'throttler',
      requestedTps: throttlerRequestedTpsControl(
        wire?.throttlerRequestedTps ?? null,
        wire?.policy.throttlerRequestedTps ?? null,
        connectionState,
      ),
      installationMode: {
        applied: wire?.throttlerInstallationMode ?? null,
        pending: null,
        applyMode: connectionState === 'connected' && wire !== null
          ? 'immediate'
          : 'unavailable',
        writable: connectionState === 'connected' && wire !== null,
        unavailableReason: connectionState === 'connected' && wire !== null
          ? null
          : UNAVAILABLE_REASON,
      },
      admittedTps: wire?.throttlerAdmittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: readerChannel(wire, connectionState, runState),
    senderChannel: senderChannel(wire),
    sender: {
      id: 'sender',
      workers: workerControl(wire?.senderWorkers ?? null),
      httpBatchSize: unavailableControl('tx'),
      timeoutMs: unavailableControl('ms'),
      workerStates: { idle: 0, inFlight: 0, backoff: 0 },
      workerSlots: null,
      retryPolicy: null,
      attemptedTps: null,
      retryAttemptedTps: null,
      terminalFailedTps: null,
      inFlightRequests: null,
      attemptsStartedTotal: 0,
      retryAttemptsStartedTotal: 0,
      successfulResponses: null,
      failedResponses: null,
      retries: null,
      timeoutsTotal: 0,
      terminalFailedBatchesTotal: 0,
      terminalFailedTransactionsTotal: 0,
      ambiguousTimeoutTransactionsTotal: 0,
      duplicateRiskTransactionsTotal: 0,
      ambiguousTerminalTransactionsTotal: 0,
      state: runState,
    },
    http: {
      id: 'http',
      connectionState: 'disconnected',
      statusCode: null,
      lastOutcome: null,
      throughputTps: null,
      inFlightRequests: null,
      requestsStartedTotal: 0,
      requestsCompletedTotal: 0,
      requestsSucceededTotal: 0,
      requestsFailedTotal: 0,
      requestsTimedOutTotal: 0,
      networkErrorsTotal: 0,
      latencyP95Ms: null,
    },
    target: {
      id: 'target',
      endpoint: null,
      artificialDelayMs: unavailableControl('ms'),
      errorRatePercent: unavailableControl('%'),
      acceptedTps: null,
      rejectedTps: null,
      latencyP95Ms: null,
      http200Responses: null,
      http503Responses: null,
      connectionState: 'disconnected',
    },
  })
}

function isWireInteger(value: unknown): value is number {
  return typeof value === 'number' &&
    Number.isFinite(value) &&
    Number.isSafeInteger(value) &&
    value >= 0
}

function isWireNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function isRangeValue(
  value: unknown,
  policy: LoadgenPolicySnapshot['readerReadBatchSize'],
): value is number {
  return isWireInteger(value) &&
    value >= policy.min &&
    value <= policy.max &&
    (value - policy.min) % policy.step === 0
}

function throttlerRequestedTpsControl(
  value: number | null,
  policy: LoadgenPolicySnapshot['throttlerRequestedTps'] | null,
  connectionState: ConnectionState,
): NumericControlSnapshot {
  if (policy === null) return unavailableControl('transactions/s')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: policy.min,
    max: policy.max,
    step: policy.step,
    unit: policy.unit,
    applyMode: connectionState === 'connected' ? 'immediate' : 'unavailable',
  }
}

function isExactObject(
  value: unknown,
  keys: readonly string[],
): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  const actual = Object.keys(value).sort()
  return actual.length === keys.length && actual.every((key, index) => key === keys[index])
}

function decodePolicy(value: unknown): LoadgenPolicySnapshot {
  if (!isExactObject(value, [
    'readerChannelCapacity',
    'readerReadBatchSize',
    'throttlerInstallationMode',
    'throttlerRequestedTps',
  ])) {
    throw new Error('snapshot policy must contain exactly reader, readerChannel, and throttler controls')
  }
  const reader = value.readerReadBatchSize
  if (!isExactObject(reader, ['default', 'max', 'min', 'mutability', 'step', 'unit'])) {
    throw new Error('snapshot reader policy is invalid')
  }
  if (
    !isWireInteger(reader.default) || !isWireInteger(reader.min) ||
    !isWireInteger(reader.max) || !isWireInteger(reader.step) ||
    reader.min <= 0 || reader.max < reader.min || reader.step <= 0 ||
    reader.unit !== 'transactions' || reader.mutability !== 'idle-only'
  ) {
    throw new Error('snapshot reader policy is invalid')
  }
  const readerPolicy = {
    default: reader.default,
    min: reader.min,
    max: reader.max,
    step: reader.step,
    unit: reader.unit,
    mutability: reader.mutability,
  }
  if (!isRangeValue(readerPolicy.default, readerPolicy)) {
    throw new Error('snapshot reader policy default is invalid')
  }

  const readerChannel = value.readerChannelCapacity
  if (!isExactObject(readerChannel, ['allowed', 'default', 'mutability', 'unit'])) {
    throw new Error('snapshot readerChannel policy is invalid')
  }
  if (
    !Array.isArray(readerChannel.allowed) || readerChannel.allowed.length === 0 ||
    !readerChannel.allowed.every(isWireInteger) || readerChannel.unit !== 'batches' ||
    readerChannel.mutability !== 'idle-only'
  ) {
    throw new Error('snapshot readerChannel policy is invalid')
  }
  const allowed = Object.freeze([...readerChannel.allowed])
  if (
    allowed.some((entry, index) => index > 0 && entry <= allowed[index - 1]!) ||
    !isWireInteger(readerChannel.default) || !allowed.includes(readerChannel.default)
  ) {
    throw new Error('snapshot readerChannel policy values are invalid')
  }
  const requestedTps = value.throttlerRequestedTps
  if (!isExactObject(requestedTps, ['default', 'max', 'min', 'mutability', 'step', 'unit'])) {
    throw new Error('snapshot throttler requested TPS policy is invalid')
  }
  if (
    !isWireInteger(requestedTps.default) || !isWireInteger(requestedTps.min) ||
    !isWireInteger(requestedTps.max) || !isWireInteger(requestedTps.step) ||
    requestedTps.max <= requestedTps.min || requestedTps.step <= 0 ||
    requestedTps.unit !== 'transactions/s' || requestedTps.mutability !== 'immediate'
  ) {
    throw new Error('snapshot throttler requested TPS policy is invalid')
  }
  const requestedTpsPolicy = {
    default: requestedTps.default,
    min: requestedTps.min,
    max: requestedTps.max,
    step: requestedTps.step,
    unit: requestedTps.unit,
    mutability: requestedTps.mutability,
  }
  if (!isRangeValue(requestedTpsPolicy.default, requestedTpsPolicy)) {
    throw new Error('snapshot throttler requested TPS policy default is invalid')
  }

  const installationMode = value.throttlerInstallationMode
  if (!isExactObject(installationMode, ['allowed', 'default', 'mutability'])) {
    throw new Error('snapshot throttler installation mode policy is invalid')
  }
  if (
    !Array.isArray(installationMode.allowed) ||
    installationMode.allowed.length !== 2 ||
    installationMode.allowed[0] !== 'installed' ||
    installationMode.allowed[1] !== 'bypass' ||
    installationMode.default !== 'installed' && installationMode.default !== 'bypass' ||
    !installationMode.allowed.includes(installationMode.default) ||
    installationMode.mutability !== 'immediate'
  ) {
    throw new Error('snapshot throttler installation mode policy is invalid')
  }
  return Object.freeze({
    readerReadBatchSize: Object.freeze(readerPolicy),
    readerChannelCapacity: Object.freeze({
      default: readerChannel.default,
      allowed,
      unit: readerChannel.unit,
      mutability: readerChannel.mutability,
    }),
    throttlerRequestedTps: Object.freeze(requestedTpsPolicy),
    throttlerInstallationMode: Object.freeze({
      default: installationMode.default,
      allowed: Object.freeze([...installationMode.allowed]),
      mutability: installationMode.mutability,
    }),
  })
}

function decodeWireSnapshot(value: unknown): WireSnapshot {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('snapshot body must be an object')
  }

  const record = value as Record<string, unknown>
  const keys = Object.keys(record).sort()
  if (
    keys.length !== WIRE_KEYS.length ||
    keys.some((key, index) => key !== WIRE_KEYS[index])
  ) {
    throw new Error('snapshot body must contain exactly forty-two wire keys')
  }

  if (
    record.runState !== 'idle' &&
    record.runState !== 'running' &&
    record.runState !== 'paused'
  ) {
    throw new Error('snapshot runState is invalid')
  }
  const policy = decodePolicy(record.policy)
  if (
    !isWireInteger(record.elapsedMs) ||
    !isWireInteger(record.totalTransactions) ||
    !isWireInteger(record.readerWorkers) ||
    !isRangeValue(record.throttlerRequestedTps, policy.throttlerRequestedTps) ||
    !isWireNumber(record.throttlerAdmittedTps) ||
    (record.throttlerInstallationMode !== 'installed' &&
      record.throttlerInstallationMode !== 'bypass') ||
    !policy.throttlerInstallationMode.allowed.includes(record.throttlerInstallationMode) ||
    !isWireInteger(record.senderWorkers) ||
    !isWireInteger(record.readerRowsRead) ||
    !isWireInteger(record.readerChannelCapacity) ||
    !policy.readerChannelCapacity.allowed.includes(record.readerChannelCapacity) ||
    !isWireInteger(record.readerChannelSentBatchesTotal) ||
    !isWireInteger(record.readerChannelSentTransactionsTotal) ||
    !isWireInteger(record.readerChannelReceivedBatchesTotal) ||
    !isWireInteger(record.readerChannelReceivedTransactionsTotal) ||
    !isWireInteger(record.readerChannelDepthBatches) ||
    !isWireInteger(record.readerChannelBufferedTransactions) ||
    !isWireInteger(record.readerChannelBlockedSenders) ||
    !isWireInteger(record.readerChannelOldestBlockedSenderMs) ||
    !isWireInteger(record.readerChannelBlockedMs) ||
    record.senderChannelCapacity !== 0 ||
    !isWireInteger(record.senderChannelSentBatchesTotal) ||
    !isWireInteger(record.senderChannelSentTransactionsTotal) ||
    !isWireInteger(record.senderChannelReceivedBatchesTotal) ||
    !isWireInteger(record.senderChannelReceivedTransactionsTotal) ||
    !isWireInteger(record.senderChannelDepthBatches) ||
    !isWireInteger(record.senderChannelBufferedTransactions) ||
    !isWireInteger(record.senderChannelBlockedSenders) ||
    !isWireInteger(record.senderChannelOldestBlockedSenderMs) ||
    !isWireInteger(record.senderChannelBlockedMs)
  ) {
    throw new Error('snapshot counters must be nonnegative safe integers')
  }
  if (!isWireNumber(record.readerReadTps)) {
    throw new Error('snapshot readerReadTps must be a nonnegative finite number')
  }
  if (
    !isWireNumber(record.readerChannelInputBatchesPerSecond) ||
    !isWireNumber(record.readerChannelInputTransactionsPerSecond) ||
    !isWireNumber(record.readerChannelOutputBatchesPerSecond) ||
    !isWireNumber(record.readerChannelOutputTransactionsPerSecond)
  ) {
    throw new Error('snapshot readerChannel rates must be nonnegative finite numbers')
  }
  if (
    !isWireNumber(record.senderChannelInputBatchesPerSecond) ||
    !isWireNumber(record.senderChannelInputTransactionsPerSecond) ||
    !isWireNumber(record.senderChannelOutputBatchesPerSecond) ||
    !isWireNumber(record.senderChannelOutputTransactionsPerSecond)
  ) {
    throw new Error('snapshot senderChannel rates must be nonnegative finite numbers')
  }
  if (!isRangeValue(record.readerReadBatchSize, policy.readerReadBatchSize)) {
    throw new Error('snapshot readerReadBatchSize is invalid')
  }
  if (
    record.startError !== null &&
    (typeof record.startError !== 'string' || record.startError.length === 0)
  ) {
    throw new Error('snapshot startError must be null or a nonempty string')
  }
  if (
    record.readerSource !== null &&
    (typeof record.readerSource !== 'string' || record.readerSource.length === 0)
  ) {
    throw new Error('snapshot readerSource must be null or a nonempty string')
  }

  return {
    runState: record.runState,
    elapsedMs: record.elapsedMs,
    startError: record.startError,
    totalTransactions: record.totalTransactions,
    policy,
    readerWorkers: record.readerWorkers,
    senderWorkers: record.senderWorkers,
    readerReadTps: record.readerReadTps,
    readerReadBatchSize: record.readerReadBatchSize,
    throttlerRequestedTps: record.throttlerRequestedTps,
    throttlerAdmittedTps: record.throttlerAdmittedTps,
    throttlerInstallationMode: record.throttlerInstallationMode,
    readerRowsRead: record.readerRowsRead,
    readerSource: record.readerSource,
    readerChannelCapacity: record.readerChannelCapacity,
    readerChannelSentBatchesTotal: record.readerChannelSentBatchesTotal,
    readerChannelSentTransactionsTotal: record.readerChannelSentTransactionsTotal,
    readerChannelReceivedBatchesTotal: record.readerChannelReceivedBatchesTotal,
    readerChannelReceivedTransactionsTotal: record.readerChannelReceivedTransactionsTotal,
    readerChannelDepthBatches: record.readerChannelDepthBatches,
    readerChannelBufferedTransactions: record.readerChannelBufferedTransactions,
    readerChannelBlockedSenders: record.readerChannelBlockedSenders,
    readerChannelOldestBlockedSenderMs: record.readerChannelOldestBlockedSenderMs,
    readerChannelBlockedMs: record.readerChannelBlockedMs,
    readerChannelInputBatchesPerSecond: record.readerChannelInputBatchesPerSecond,
    readerChannelInputTransactionsPerSecond: record.readerChannelInputTransactionsPerSecond,
    readerChannelOutputBatchesPerSecond: record.readerChannelOutputBatchesPerSecond,
    readerChannelOutputTransactionsPerSecond: record.readerChannelOutputTransactionsPerSecond,
    senderChannelCapacity: record.senderChannelCapacity,
    senderChannelSentBatchesTotal: record.senderChannelSentBatchesTotal,
    senderChannelSentTransactionsTotal: record.senderChannelSentTransactionsTotal,
    senderChannelReceivedBatchesTotal: record.senderChannelReceivedBatchesTotal,
    senderChannelReceivedTransactionsTotal: record.senderChannelReceivedTransactionsTotal,
    senderChannelDepthBatches: record.senderChannelDepthBatches,
    senderChannelBufferedTransactions: record.senderChannelBufferedTransactions,
    senderChannelBlockedSenders: record.senderChannelBlockedSenders,
    senderChannelOldestBlockedSenderMs: record.senderChannelOldestBlockedSenderMs,
    senderChannelBlockedMs: record.senderChannelBlockedMs,
    senderChannelInputBatchesPerSecond: record.senderChannelInputBatchesPerSecond,
    senderChannelInputTransactionsPerSecond: record.senderChannelInputTransactionsPerSecond,
    senderChannelOutputBatchesPerSecond: record.senderChannelOutputBatchesPerSecond,
    senderChannelOutputTransactionsPerSecond: record.senderChannelOutputTransactionsPerSecond,
  }
}

function readBatchSizeControl(
  value: number | null,
  policy: LoadgenPolicySnapshot['readerReadBatchSize'] | null,
  connectionState: ConnectionState,
  runState: RunState,
): NumericControlSnapshot {
  if (policy === null) return unavailableControl('transactions')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: policy.min,
    max: policy.max,
    step: policy.step,
    unit: policy.unit,
    applyMode: connectionState === 'connected' && runState === 'idle'
      ? 'immediate'
      : 'unavailable',
  }
}

function canDispatchPolicyCommand(
  command: SupportedCommand,
  snapshot: LoadgenTelemetrySnapshot,
): boolean {
  const policy = snapshot.policy
  if (command.type === 'reset') return true
  if (snapshot.connectionState === 'error') return false
  if (command.type === 'run' || command.type === 'pause') {
    return snapshot.connectionState === 'connecting' || policy !== null
  }
  if (command.type === 'set-requested-tps') {
    return snapshot.connectionState === 'connected' && policy !== null &&
      isRangeValue(command.value, policy.throttlerRequestedTps)
  }
  if (command.type === 'set-throttler-installation-mode') {
    return snapshot.connectionState === 'connected' && policy !== null &&
      policy.throttlerInstallationMode.allowed.includes(command.value)
  }
  if (
    snapshot.connectionState !== 'connected' ||
    policy === null ||
    snapshot.runState !== 'idle'
  ) return false
  if (command.type === 'set-read-batch-size') {
    return isRangeValue(command.value, policy.readerReadBatchSize)
  }
  return policy.readerChannelCapacity.allowed.includes(command.value)
}

async function decodeResponse(response: Response): Promise<WireSnapshot> {
  if (response.status < 200 || response.status > 299) {
    throw new Error('snapshot response must be successful')
  }

  const mediaType = response.headers
    .get('Content-Type')
    ?.split(';', 1)[0]
    .trim()
    .toLowerCase()
  if (mediaType !== 'application/json') {
    throw new Error('snapshot response must be JSON')
  }

  return decodeWireSnapshot(await response.json())
}

async function decodeCommandError(response: Response): Promise<string | null> {
  try {
    const body: unknown = await response.json()
    if (body === null || typeof body !== 'object' || Array.isArray(body)) {
      return null
    }
    const error = (body as Record<string, unknown>).error
    return typeof error === 'string' && error.length > 0 ? error : null
  } catch {
    return null
  }
}

export class HttpAdapter implements LoadgenAdapter {
  readonly kind = 'http' as const

  private readonly listeners = new Set<LoadgenSnapshotListener>()
  private snapshot: LoadgenTelemetrySnapshot = createSnapshot(
    0,
    'connecting',
    null,
  )
  private timer: ReturnType<typeof setInterval> | null
  private activeController: AbortController | null = null
  private requestInFlight = false
  private commandSequence = 0
  private commandChannel: Promise<void> = Promise.resolve()
  private disposed = false

  constructor() {
    this.timer = setInterval(this.poll, POLL_INTERVAL_MS)
    void this.poll()
  }

  getSnapshot = (): LoadgenTelemetrySnapshot => this.snapshot

  subscribe = (listener: LoadgenSnapshotListener): (() => void) => {
    if (this.disposed) return () => undefined

    this.listeners.add(listener)
    listener(this.snapshot)
    let subscribed = true

    return () => {
      if (!subscribed) return
      subscribed = false
      this.listeners.delete(listener)
    }
  }

  dispatch = (command: LoadgenCommand): Promise<CommandReceipt> => {
    const commandId = `http-local-${++this.commandSequence}`

    if (!isSupportedCommand(command)) {
      return Promise.resolve(this.rejectCommand(
        commandId,
        command,
        'unavailable',
        UNAVAILABLE_COMMAND_MESSAGE,
        false,
      ))
    }

    if (this.disposed) {
      return Promise.resolve(this.rejectCommand(
        commandId,
        command,
        'disposed',
        DISPOSED_COMMAND_MESSAGE,
        false,
      ))
    }

    if (!canDispatchPolicyCommand(command, this.snapshot)) {
      return Promise.resolve(this.rejectCommand(
        commandId,
        command,
        'unavailable',
        UNAVAILABLE_COMMAND_MESSAGE,
        false,
      ))
    }

    const receipt = this.commandChannel.then(
      () => this.sendCommand(commandId, command),
    )
    this.commandChannel = receipt.then(
      () => undefined,
      () => undefined,
    )
    return receipt
  }

  dispose = (): void => {
    if (this.disposed) return
    this.disposed = true

    if (this.timer !== null) {
      clearInterval(this.timer)
      this.timer = null
    }
    this.activeController?.abort()
    this.listeners.clear()
  }

  private poll = async (): Promise<void> => {
    if (this.disposed || this.requestInFlight) return

    this.requestInFlight = true
    const controller = new AbortController()
    this.activeController = controller
    let wire: WireSnapshot | null = null
    let failed = false

    try {
      const response = await fetch(SNAPSHOT_ENDPOINT, {
        method: 'GET',
        headers: { Accept: 'application/json' },
        signal: controller.signal,
      })
      if (this.disposed) return

      wire = await decodeResponse(response)
      if (this.disposed) return
    } catch {
      if (this.disposed) return
      failed = true
    } finally {
      if (this.activeController === controller) {
        this.activeController = null
      }
      this.requestInFlight = false
    }

    if (this.disposed) return

    const revision = this.snapshot.revision + 1
    if (failed || wire === null) {
      this.publish(createSnapshot(revision, 'error', null))
      return
    }

    this.publish(createSnapshot(revision, 'connected', wire))
  }

  private sendCommand = async (
    commandId: string,
    command: SupportedCommand,
  ): Promise<CommandReceipt> => {
    if (this.disposed) {
      return this.rejectCommand(
        commandId,
        command,
        'disposed',
        DISPOSED_COMMAND_MESSAGE,
        false,
      )
    }

    if (!canDispatchPolicyCommand(command, this.snapshot)) {
      return this.rejectCommand(
        commandId,
        command,
        'unavailable',
        UNAVAILABLE_COMMAND_MESSAGE,
        false,
      )
    }

    try {
      const response = await fetch(COMMAND_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(
          command.type === 'set-read-batch-size' ||
          command.type === 'set-reader-channel-capacity' ||
          command.type === 'set-requested-tps' ||
          command.type === 'set-throttler-installation-mode'
            ? { action: command.type, value: command.value }
            : { action: command.type },
        ),
      })

      if (response.status >= 200 && response.status <= 299) {
        return deepFreeze({
          commandId,
          commandType: command.type,
          accepted: true,
          applyMode: 'immediate',
          appliedAtMs: Date.now(),
          snapshotRevision: this.snapshot.revision,
          error: null,
        })
      }

      if (response.status === 422) {
        const message = await decodeCommandError(response)
        if (message !== null) {
          return this.rejectCommand(
            commandId,
            command,
            'unavailable',
            message,
            false,
          )
        }
      }

      return this.rejectCommand(
        commandId,
        command,
        'unavailable',
        `command request failed with HTTP status ${response.status}`,
        response.status >= 500 && response.status <= 599,
      )
    } catch {
      return this.rejectCommand(
        commandId,
        command,
        'unavailable',
        NETWORK_COMMAND_MESSAGE,
        true,
      )
    }
  }

  private rejectCommand(
    commandId: string,
    command: LoadgenCommand,
    code: 'disposed' | 'unavailable',
    message: string,
    retryable: boolean,
  ): CommandReceipt {
    return deepFreeze({
      commandId,
      commandType: command.type,
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: this.snapshot.revision,
      error: {
        code,
        message,
        retryable,
        details: null,
      },
    })
  }

  private publish(snapshot: LoadgenTelemetrySnapshot): void {
    this.snapshot = snapshot
    this.listeners.forEach((listener) => listener(snapshot))
  }
}
