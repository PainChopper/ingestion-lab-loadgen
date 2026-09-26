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
  ReaderSourceErrorSnapshot,
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
  'policy', 'reader', 'readerChannel', 'run', 'sender', 'senderChannel', 'throttler',
])
const RUN_KEYS = Object.freeze(['elapsedMs', 'state', 'totalTransactions'])
const READER_KEYS = Object.freeze(['blockedWorkers', 'drainingBlockedWorkers', 'drainingIdleWorkers', 'drainingReadingWorkers', 'drainingWorkers', 'idleWorkers', 'liveWorkers', 'readBatchSize', 'readTps', 'readingWorkers', 'rowsRead', 'sourceDirectory', 'sourceError', 'workers'])
const SOURCE_ERROR_KEYS = Object.freeze(['category', 'message', 'operation', 'relativePath'])
const THROTTLER_KEYS = Object.freeze(['admittedTps', 'installationMode', 'requestedTps'])
const SENDER_KEYS = Object.freeze(['backoffWorkers', 'drainingBackoffWorkers', 'drainingIdleWorkers', 'drainingInFlightWorkers', 'drainingWorkers', 'idleWorkers', 'inFlightWorkers', 'liveWorkers', 'workers'])
const CHANNEL_KEYS = Object.freeze([
  'blockedMs', 'blockedSenders', 'bufferedTransactions', 'capacity', 'depthBatches',
  'inputBatchesPerSecond', 'inputTransactionsPerSecond', 'oldestBlockedSenderMs',
  'outputBatchesPerSecond', 'outputTransactionsPerSecond', 'receivedBatchesTotal',
  'receivedTransactionsTotal', 'sentBatchesTotal', 'sentTransactionsTotal',
])

interface WireRun { readonly state: RunState; readonly totalTransactions: number; readonly elapsedMs: number }
interface WireReader { readonly workers: number; readonly liveWorkers: number; readonly idleWorkers: number; readonly readingWorkers: number; readonly blockedWorkers: number; readonly drainingWorkers: number; readonly drainingIdleWorkers: number; readonly drainingReadingWorkers: number; readonly drainingBlockedWorkers: number; readonly readBatchSize: number; readonly readTps: number; readonly rowsRead: number; readonly sourceDirectory: string; readonly sourceError: ReaderSourceErrorSnapshot | null }
interface WireThrottler { readonly requestedTps: number; readonly admittedTps: number; readonly installationMode: ThrottlerInstallationMode }
interface WireSender { readonly workers: number; readonly liveWorkers: number; readonly idleWorkers: number; readonly inFlightWorkers: number; readonly backoffWorkers: number; readonly drainingWorkers: number; readonly drainingIdleWorkers: number; readonly drainingInFlightWorkers: number; readonly drainingBackoffWorkers: number }
interface WireChannelSnapshot { readonly capacity: number; readonly depthBatches: number; readonly bufferedTransactions: number; readonly blockedSenders: number; readonly oldestBlockedSenderMs: number; readonly blockedMs: number; readonly sentBatchesTotal: number; readonly sentTransactionsTotal: number; readonly receivedBatchesTotal: number; readonly receivedTransactionsTotal: number; readonly inputBatchesPerSecond: number; readonly inputTransactionsPerSecond: number; readonly outputBatchesPerSecond: number; readonly outputTransactionsPerSecond: number }
interface WireSnapshot { readonly run: WireRun; readonly reader: WireReader; readonly throttler: WireThrottler; readonly sender: WireSender; readonly readerChannel: WireChannelSnapshot; readonly senderChannel: WireChannelSnapshot; readonly policy: LoadgenPolicySnapshot }

type SupportedCommand = Extract<
  LoadgenCommand,
  {
    type:
      | 'run'
      | 'pause'
      | 'reset'
      | 'set-reader-channel-capacity'
      | 'set-sender-channel-capacity'
      | 'set-read-batch-size'
      | 'set-requested-tps'
      | 'set-throttler-installation-mode'
      | 'set-worker-count'
      | 'set-sender-workers'
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
    command.type === 'set-sender-channel-capacity' ||
    command.type === 'set-read-batch-size' ||
    command.type === 'set-requested-tps' ||
    command.type === 'set-throttler-installation-mode' ||
    command.type === 'set-worker-count' ||
    command.type === 'set-sender-workers'
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

function senderControl(
  value: number | null,
  policy: LoadgenPolicySnapshot['senderWorkers'] | null,
  connectionState: ConnectionState,
  unavailableUnit = 'workers',
): NumericControlSnapshot {
  if (policy === null) return unavailableControl(unavailableUnit)
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
      wire.readerChannel.capacity,
      wire.policy.readerChannelCapacity,
      connectionState,
      runState,
    ),
    depthBatches: wire.readerChannel.depthBatches,
    bufferedTransactions: wire.readerChannel.bufferedTransactions,
    sentBatchesTotal: wire.readerChannel.sentBatchesTotal,
    sentTransactionsTotal: wire.readerChannel.sentTransactionsTotal,
    receivedBatchesTotal: wire.readerChannel.receivedBatchesTotal,
    receivedTransactionsTotal: wire.readerChannel.receivedTransactionsTotal,
    inputBatchesPerSecond: wire.readerChannel.inputBatchesPerSecond,
    inputTransactionsPerSecond: wire.readerChannel.inputTransactionsPerSecond,
    outputBatchesPerSecond: wire.readerChannel.outputBatchesPerSecond,
    outputTransactionsPerSecond: wire.readerChannel.outputTransactionsPerSecond,
    inputTps: wire.readerChannel.inputTransactionsPerSecond,
    outputTps: wire.readerChannel.outputTransactionsPerSecond,
    throughputTps: wire.readerChannel.outputTransactionsPerSecond,
    blockedSenders: wire.readerChannel.blockedSenders,
    oldestBlockedSenderMs: wire.readerChannel.oldestBlockedSenderMs,
    blockedMs: wire.readerChannel.blockedMs,
  }
}

function senderChannel(
  wire: WireSnapshot | null,
  connectionState: ConnectionState,
  runState: RunState,
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'throttler-to-sender',
    'throttler',
    'sender',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: readerChannelCapacityControl(
      wire.senderChannel.capacity,
      wire.policy.senderChannelCapacity,
      connectionState,
      runState,
    ),
    depthBatches: wire.senderChannel.depthBatches,
    bufferedTransactions: wire.senderChannel.bufferedTransactions,
    sentBatchesTotal: wire.senderChannel.sentBatchesTotal,
    sentTransactionsTotal: wire.senderChannel.sentTransactionsTotal,
    receivedBatchesTotal: wire.senderChannel.receivedBatchesTotal,
    receivedTransactionsTotal: wire.senderChannel.receivedTransactionsTotal,
    inputBatchesPerSecond: wire.senderChannel.inputBatchesPerSecond,
    inputTransactionsPerSecond: wire.senderChannel.inputTransactionsPerSecond,
    outputBatchesPerSecond: wire.senderChannel.outputBatchesPerSecond,
    outputTransactionsPerSecond: wire.senderChannel.outputTransactionsPerSecond,
    inputTps: wire.senderChannel.inputTransactionsPerSecond,
    outputTps: wire.senderChannel.outputTransactionsPerSecond,
    throughputTps: wire.senderChannel.outputTransactionsPerSecond,
    blockedSenders: wire.senderChannel.blockedSenders,
    oldestBlockedSenderMs: wire.senderChannel.oldestBlockedSenderMs,
    blockedMs: wire.senderChannel.blockedMs,
  }
}

function createSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: WireSnapshot | null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.run.state ?? 'idle'

  return deepFreeze({
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.run.elapsedMs ?? 0,
    totalTransactions: wire?.run.totalTransactions ?? 0,
    policy: wire?.policy ?? null,
    reader: {
      id: 'reader',
      workers: senderControl(wire?.reader.workers ?? null, wire?.policy.readerWorkers ?? null, connectionState),
      liveWorkers: wire?.reader.liveWorkers ?? 0,
      drainingWorkers: wire?.reader.drainingWorkers ?? 0,
      idleWorkers: wire?.reader.idleWorkers ?? 0,
      readingWorkers: wire?.reader.readingWorkers ?? 0,
      blockedWorkers: wire?.reader.blockedWorkers ?? 0,
      drainingIdleWorkers: wire?.reader.drainingIdleWorkers ?? 0,
      drainingReadingWorkers: wire?.reader.drainingReadingWorkers ?? 0,
      drainingBlockedWorkers: wire?.reader.drainingBlockedWorkers ?? 0,
      readBatchSize: readBatchSizeControl(
        wire?.reader.readBatchSize ?? null,
        wire?.policy.readerReadBatchSize ?? null,
        connectionState,
        runState,
      ),
      readTps: wire?.reader.readTps ?? null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: wire?.reader.rowsRead ?? null,
      sourceDirectory: wire?.reader.sourceDirectory,
      sourceError: wire?.reader.sourceError ?? null,
      state: runState,
    },
    throttler: {
      id: 'throttler',
      requestedTps: throttlerRequestedTpsControl(
        wire?.throttler.requestedTps ?? null,
        wire?.policy.throttlerRequestedTps ?? null,
        connectionState,
      ),
      installationMode: {
        applied: wire?.throttler.installationMode ?? null,
        pending: null,
        applyMode: connectionState === 'connected' && wire !== null
          ? 'immediate'
          : 'unavailable',
        writable: connectionState === 'connected' && wire !== null,
        unavailableReason: connectionState === 'connected' && wire !== null
          ? null
          : UNAVAILABLE_REASON,
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: readerChannel(wire, connectionState, runState),
    senderChannel: senderChannel(wire, connectionState, runState),
    sender: {
      id: 'sender',
      workers: senderControl(wire?.sender.workers ?? null, wire?.policy.senderWorkers ?? null, connectionState),
      liveWorkers: wire?.sender.liveWorkers ?? 0,
      drainingWorkers: wire?.sender.drainingWorkers ?? 0,
      timeoutMs: unavailableControl('ms'),
      idleWorkers: wire?.sender.idleWorkers ?? 0,
      inFlightWorkers: wire?.sender.inFlightWorkers ?? 0,
      backoffWorkers: wire?.sender.backoffWorkers ?? 0,
      drainingIdleWorkers: wire?.sender.drainingIdleWorkers ?? 0,
      drainingInFlightWorkers: wire?.sender.drainingInFlightWorkers ?? 0,
      drainingBackoffWorkers: wire?.sender.drainingBackoffWorkers ?? 0,
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
		'logging',
    'metricsWindowMs',
    'readerChannelCapacity',
    'readerReadBatchSize', 'readerWorkers',
    'senderChannelCapacity',
    'senderRetry',
    'senderWorkers',
    'throttlerInstallationMode',
    'throttlerRequestedTps',
  ])) {
    throw new Error('snapshot policy must contain exactly reader, readerChannel, senderChannel, throttler, and metrics controls')
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
  const readerWorkers = value.readerWorkers
  if (!isExactObject(readerWorkers, ['default', 'max', 'min', 'mutability', 'step', 'unit']) ||
    !isWireInteger(readerWorkers.default) || !isWireInteger(readerWorkers.min) || !isWireInteger(readerWorkers.max) || !isWireInteger(readerWorkers.step) ||
    readerWorkers.min <= 0 || readerWorkers.max < readerWorkers.min || readerWorkers.step <= 0 || readerWorkers.unit !== 'workers' || readerWorkers.mutability !== 'immediate' ||
    !isRangeValue(readerWorkers.default, readerWorkers as unknown as NonNullable<LoadgenPolicySnapshot['readerWorkers']>)) throw new Error('snapshot reader workers policy is invalid')

  const metricsWindow = value.metricsWindowMs
  if (!isExactObject(metricsWindow, ['default', 'max', 'min', 'mutability', 'step', 'unit'])) {
    throw new Error('snapshot metrics window policy is invalid')
  }
  if (
    !isWireInteger(metricsWindow.default) || !isWireInteger(metricsWindow.min) ||
    !isWireInteger(metricsWindow.max) || !isWireInteger(metricsWindow.step) ||
    metricsWindow.min < 100 || metricsWindow.max > 10_000 ||
    metricsWindow.max < metricsWindow.min || metricsWindow.step <= 0 ||
    metricsWindow.unit !== 'milliseconds' || metricsWindow.mutability !== 'startup-only'
  ) {
    throw new Error('snapshot metrics window policy is invalid')
  }
  const metricsWindowPolicy = {
    default: metricsWindow.default,
    min: metricsWindow.min,
    max: metricsWindow.max,
    step: metricsWindow.step,
    unit: metricsWindow.unit,
    mutability: metricsWindow.mutability,
  }
  if (!isRangeValue(metricsWindowPolicy.default, metricsWindowPolicy)) {
    throw new Error('snapshot metrics window policy default is invalid')
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
  const senderChannel = value.senderChannelCapacity
  if (!isExactObject(senderChannel, ['allowed', 'default', 'mutability', 'unit'])) {
    throw new Error('snapshot senderChannel policy is invalid')
  }
  if (
    !Array.isArray(senderChannel.allowed) || senderChannel.allowed.length === 0 ||
    !senderChannel.allowed.every(isWireInteger) || senderChannel.unit !== 'batches' ||
    senderChannel.mutability !== 'idle-only'
  ) {
    throw new Error('snapshot senderChannel policy is invalid')
  }
  const senderAllowed = Object.freeze([...senderChannel.allowed])
  if (
    senderAllowed.some((entry, index) => index > 0 && entry <= senderAllowed[index - 1]!) ||
    !isWireInteger(senderChannel.default) ||
    !senderAllowed.includes(senderChannel.default)
  ) {
    throw new Error('snapshot senderChannel policy values are invalid')
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
  const senderRanges = [
    [value.senderWorkers, 32, 1, 32, 1, 'workers'],
  ] as const
  for (const [senderRange, defaultValue, min, max, step, unit] of senderRanges) {
    if (!isExactObject(senderRange, ['default', 'max', 'min', 'mutability', 'step', 'unit']) ||
      senderRange.default !== defaultValue || senderRange.min !== min ||
      senderRange.max !== max || senderRange.step !== step ||
      senderRange.unit !== unit || senderRange.mutability !== 'immediate') {
      throw new Error('snapshot Sender policy is invalid')
    }
  }
  const retry = value.senderRetry
  if (!isExactObject(retry, ['backoffBaseMs', 'backoffMultiplier', 'jitterPercent', 'maxAttempts', 'mutability']) ||
    retry.maxAttempts !== 3 || retry.backoffBaseMs !== 250 ||
    retry.backoffMultiplier !== 2 || retry.jitterPercent !== 20 ||
    retry.mutability !== 'startup-only') {
    throw new Error('snapshot Sender retry policy is invalid')
  }
	const logging = value.logging
	if (!isExactObject(logging, ['level', 'mutability']) ||
		(logging.level !== 'debug' && logging.level !== 'info' && logging.level !== 'warn' && logging.level !== 'error') ||
		logging.mutability !== 'startup-only') {
		throw new Error('snapshot logging policy is invalid')
	}
  return Object.freeze({
    readerReadBatchSize: Object.freeze(readerPolicy),
    readerWorkers: Object.freeze(readerWorkers as unknown as LoadgenPolicySnapshot['readerWorkers']),
    metricsWindowMs: Object.freeze(metricsWindowPolicy),
    readerChannelCapacity: Object.freeze({
      default: readerChannel.default,
      allowed,
      unit: readerChannel.unit,
      mutability: readerChannel.mutability,
    }),
    senderChannelCapacity: Object.freeze({
      default: senderChannel.default,
      allowed: senderAllowed,
      unit: senderChannel.unit,
      mutability: senderChannel.mutability,
    }),
    throttlerRequestedTps: Object.freeze(requestedTpsPolicy),
    throttlerInstallationMode: Object.freeze({
      default: installationMode.default,
      allowed: Object.freeze([...installationMode.allowed]),
      mutability: installationMode.mutability,
    }),
    senderWorkers: Object.freeze(value.senderWorkers as unknown as LoadgenPolicySnapshot['senderWorkers']),
    senderRetry: Object.freeze(retry as unknown as LoadgenPolicySnapshot['senderRetry']),
		logging: Object.freeze(logging as unknown as LoadgenPolicySnapshot['logging']),
  })
}

function decodeChannelSnapshot(
  value: unknown,
  allowedCapacity: readonly number[],
): WireChannelSnapshot {
  if (!isExactObject(value, CHANNEL_KEYS)) throw new Error('snapshot channel is invalid')
  if (
    !isWireInteger(value.capacity) || !allowedCapacity.includes(value.capacity) ||
    !isWireInteger(value.depthBatches) || !isWireInteger(value.bufferedTransactions) ||
    !isWireInteger(value.blockedSenders) || !isWireInteger(value.oldestBlockedSenderMs) ||
    !isWireInteger(value.blockedMs) || !isWireInteger(value.sentBatchesTotal) ||
    !isWireInteger(value.sentTransactionsTotal) || !isWireInteger(value.receivedBatchesTotal) ||
    !isWireInteger(value.receivedTransactionsTotal) ||
    !isWireNumber(value.inputBatchesPerSecond) || !isWireNumber(value.inputTransactionsPerSecond) ||
    !isWireNumber(value.outputBatchesPerSecond) || !isWireNumber(value.outputTransactionsPerSecond)
  ) throw new Error('snapshot channel values are invalid')
  return value as unknown as WireChannelSnapshot
}

function decodeWireSnapshot(value: unknown): WireSnapshot {
  if (!isExactObject(value, WIRE_KEYS)) throw new Error('snapshot body must contain exactly seven sections')
  if (!isExactObject(value.run, RUN_KEYS)) throw new Error('snapshot run is invalid')
  if (!isExactObject(value.reader, READER_KEYS)) throw new Error('snapshot reader is invalid')
  if (!isExactObject(value.throttler, THROTTLER_KEYS)) throw new Error('snapshot throttler is invalid')
  if (!isExactObject(value.sender, SENDER_KEYS)) throw new Error('snapshot sender is invalid')
  const policy = decodePolicy(value.policy)
  const run = value.run
  const reader = value.reader
  const throttler = value.throttler
  const sender = value.sender
  if (run.state !== 'idle' && run.state !== 'running' && run.state !== 'paused' && run.state !== 'faulted') throw new Error('snapshot run state is invalid')
  if (!isWireInteger(run.elapsedMs) || !isWireInteger(run.totalTransactions)) throw new Error('snapshot run values are invalid')
  if (!isRangeValue(reader.workers, policy.readerWorkers!) || !isRangeValue(reader.readBatchSize, policy.readerReadBatchSize) || !isWireNumber(reader.readTps) || !isWireInteger(reader.rowsRead) || !isWireInteger(reader.liveWorkers) || !isWireInteger(reader.idleWorkers) || !isWireInteger(reader.readingWorkers) || !isWireInteger(reader.blockedWorkers) || !isWireInteger(reader.drainingWorkers) || !isWireInteger(reader.drainingIdleWorkers) || !isWireInteger(reader.drainingReadingWorkers) || !isWireInteger(reader.drainingBlockedWorkers) || reader.liveWorkers !== reader.idleWorkers + reader.readingWorkers + reader.blockedWorkers || reader.drainingWorkers !== reader.drainingIdleWorkers + reader.drainingReadingWorkers + reader.drainingBlockedWorkers || reader.drainingIdleWorkers > reader.idleWorkers || reader.drainingReadingWorkers > reader.readingWorkers || reader.drainingBlockedWorkers > reader.blockedWorkers || ((run.state === 'idle' || run.state === 'faulted') && (reader.liveWorkers !== 0 || reader.drainingWorkers !== 0))) throw new Error('snapshot reader values are invalid')
  if (typeof reader.sourceDirectory !== 'string' || reader.sourceDirectory.length === 0) throw new Error('snapshot reader sourceDirectory is invalid')
  if (reader.sourceError !== null && (!isExactObject(reader.sourceError, SOURCE_ERROR_KEYS) || reader.sourceError.category !== 'source' || (reader.sourceError.operation !== 'glob' && reader.sourceError.operation !== 'open' && reader.sourceError.operation !== 'read' && reader.sourceError.operation !== 'close' && reader.sourceError.operation !== 'reader-close') || typeof reader.sourceError.relativePath !== 'string' || reader.sourceError.relativePath.length === 0 || reader.sourceError.relativePath.startsWith('/') || reader.sourceError.relativePath.startsWith('../') || typeof reader.sourceError.message !== 'string' || reader.sourceError.message.length === 0)) throw new Error('snapshot reader sourceError is invalid')
  if (!isRangeValue(throttler.requestedTps, policy.throttlerRequestedTps) || !isWireNumber(throttler.admittedTps) || (throttler.installationMode !== 'installed' && throttler.installationMode !== 'bypass') || !policy.throttlerInstallationMode.allowed.includes(throttler.installationMode)) throw new Error('snapshot throttler values are invalid')
  if (!isRangeValue(sender.workers, policy.senderWorkers) ||
    !isWireInteger(sender.liveWorkers) || !isWireInteger(sender.idleWorkers) || !isWireInteger(sender.inFlightWorkers) || !isWireInteger(sender.backoffWorkers) || !isWireInteger(sender.drainingWorkers) || !isWireInteger(sender.drainingIdleWorkers) || !isWireInteger(sender.drainingInFlightWorkers) || !isWireInteger(sender.drainingBackoffWorkers) ||
    sender.liveWorkers !== sender.idleWorkers + sender.inFlightWorkers + sender.backoffWorkers || sender.drainingWorkers !== sender.drainingIdleWorkers + sender.drainingInFlightWorkers + sender.drainingBackoffWorkers || sender.drainingIdleWorkers > sender.idleWorkers || sender.drainingInFlightWorkers > sender.inFlightWorkers || sender.drainingBackoffWorkers > sender.backoffWorkers ||
    ((run.state === 'idle' || run.state === 'paused' || run.state === 'faulted') &&
      (sender.liveWorkers !== 0 || sender.drainingWorkers !== 0))) {
    throw new Error('snapshot sender values are invalid')
  }
  return {
    run: run as unknown as WireRun,
    reader: reader as unknown as WireReader,
    throttler: throttler as unknown as WireThrottler,
    sender: sender as unknown as WireSender,
    readerChannel: decodeChannelSnapshot(value.readerChannel, policy.readerChannelCapacity.allowed),
    senderChannel: decodeChannelSnapshot(value.senderChannel, policy.senderChannelCapacity.allowed),
    policy,
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
  if (command.type === 'set-sender-workers') {
    return snapshot.connectionState === 'connected' && policy !== null &&
      isRangeValue(command.value, policy.senderWorkers)
  }
  if (command.type === 'set-worker-count' && command.actor === 'reader') {
    return snapshot.connectionState === 'connected' && policy?.readerWorkers !== undefined && isRangeValue(command.value, policy.readerWorkers)
  }
  if (
    snapshot.connectionState !== 'connected' ||
    policy === null ||
    snapshot.runState !== 'idle'
  ) return false
  if (command.type === 'set-read-batch-size') {
    return isRangeValue(command.value, policy.readerReadBatchSize)
  }
  return command.type === 'set-reader-channel-capacity'
    ? policy.readerChannelCapacity.allowed.includes(command.value)
    : policy.senderChannelCapacity.allowed.includes(command.value)
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
          command.type === 'set-sender-channel-capacity' ||
          command.type === 'set-requested-tps' ||
          command.type === 'set-throttler-installation-mode' ||
          command.type === 'set-sender-workers'
            ? { action: command.type, value: command.value }
            : command.type === 'set-worker-count' && command.actor === 'reader'
              ? { action: 'set-reader-workers', value: command.value }
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
