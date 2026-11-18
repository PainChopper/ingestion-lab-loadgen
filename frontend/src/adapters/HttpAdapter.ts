import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  AllowedControlConfigSnapshot,
  CommandReceipt,
  ConnectionState,
  LoadgenCommand,
  LoadgenConfigSnapshot,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  RangeControlConfigSnapshot,
  ChannelTelemetrySnapshot,
  ReaderSourceErrorSnapshot,
} from '../model/loadgen'
const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const COMMAND_ENDPOINT = '/api/loadgen/commands'
const POLL_INTERVAL_MS = 1_000
const UNAVAILABLE_COMMAND_MESSAGE = 'command is not available in the HTTP adapter'
const DISPOSED_COMMAND_MESSAGE = 'http adapter is disposed'
const WIRE_KEYS = Object.freeze([
  'config', 'reader', 'readerChannel', 'run', 'sender', 'senderChannel', 'throttler',
])
const RUN_KEYS = Object.freeze(['elapsedMs', 'state', 'totalTransactions'])
const READER_KEYS = Object.freeze(['blockedWorkers', 'drainingBlockedWorkers', 'drainingIdleWorkers', 'drainingReadingWorkers', 'drainingWorkers', 'idleWorkers', 'liveWorkers', 'readBatchSize', 'readTps', 'readingWorkers', 'rowsRead', 'sourceDirectory', 'sourceError', 'workers'])
const SOURCE_ERROR_KEYS = Object.freeze(['category', 'message', 'operation', 'relativePath'])
const THROTTLER_KEYS = Object.freeze(['admittedTps', 'installed', 'requestedTps'])
const SENDER_KEYS = Object.freeze(['backoffWorkers', 'drainingBackoffWorkers', 'drainingIdleWorkers', 'drainingInFlightWorkers', 'drainingWorkers', 'idleWorkers', 'inFlightWorkers', 'liveWorkers', 'workers'])
const CHANNEL_KEYS = Object.freeze([
  'blockedMs', 'blockedSenders', 'bufferedTransactions', 'capacity', 'depthBatches',
  'inputBatchesPerSecond', 'inputTransactionsPerSecond', 'oldestBlockedSenderMs',
  'outputBatchesPerSecond', 'outputTransactionsPerSecond', 'receivedBatchesTotal',
  'receivedTransactionsTotal', 'sentBatchesTotal', 'sentTransactionsTotal',
])
const RANGE_CONFIG_KEYS = Object.freeze(['initial', 'max', 'min', 'mutability', 'step', 'unit'])
const ALLOWED_CONFIG_KEYS = Object.freeze(['allowed', 'initial', 'mutability', 'unit'])
const CONFIG_KEYS = Object.freeze([
  'logging', 'metricsWindowMs', 'readerChannelCapacity', 'readerReadBatchSize',
  'readerWorkers', 'senderChannelCapacity', 'senderWorkers', 'throttlerInstalled',
  'throttlerRequestedTps',
])

type WireRunState = 'starting' | 'running' | 'stopping' | 'faulted'
interface WireRun { readonly state: WireRunState; readonly totalTransactions: number; readonly elapsedMs: number }
interface WireReader { readonly workers: number; readonly liveWorkers: number; readonly idleWorkers: number; readonly readingWorkers: number; readonly blockedWorkers: number; readonly drainingWorkers: number; readonly drainingIdleWorkers: number; readonly drainingReadingWorkers: number; readonly drainingBlockedWorkers: number; readonly readBatchSize: number; readonly readTps: number; readonly rowsRead: number; readonly sourceDirectory: string; readonly sourceError: ReaderSourceErrorSnapshot | null }
interface WireThrottler { readonly requestedTps: number; readonly admittedTps: number; readonly installed: boolean }
interface WireSender { readonly workers: number; readonly liveWorkers: number; readonly idleWorkers: number; readonly inFlightWorkers: number; readonly backoffWorkers: number; readonly drainingWorkers: number; readonly drainingIdleWorkers: number; readonly drainingInFlightWorkers: number; readonly drainingBackoffWorkers: number }
interface WireChannelSnapshot { readonly capacity: number; readonly depthBatches: number; readonly bufferedTransactions: number; readonly blockedSenders: number; readonly oldestBlockedSenderMs: number; readonly blockedMs: number; readonly sentBatchesTotal: number; readonly sentTransactionsTotal: number; readonly receivedBatchesTotal: number; readonly receivedTransactionsTotal: number; readonly inputBatchesPerSecond: number; readonly inputTransactionsPerSecond: number; readonly outputBatchesPerSecond: number; readonly outputTransactionsPerSecond: number }
interface WireSnapshot { readonly run: WireRun; readonly reader: WireReader; readonly throttler: WireThrottler; readonly sender: WireSender; readonly readerChannel: WireChannelSnapshot; readonly senderChannel: WireChannelSnapshot; readonly config: LoadgenConfigSnapshot }

type SupportedCommand = Extract<
  LoadgenCommand,
  {
    type:
      | 'set-requested-tps'
      | 'set-throttler-installed'
      | 'set-reader-workers'
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
  return command.type === 'set-requested-tps' ||
    command.type === 'set-throttler-installed' ||
    command.type === 'set-reader-workers' ||
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
  config: LoadgenConfigSnapshot['senderWorkers'] | null,
  writable: boolean,
  unavailableUnit = 'workers',
): NumericControlSnapshot {
  if (config === null) return unavailableControl(unavailableUnit)
  return {
    applied: value,
    preview: null,
    pending: null,
    min: config.min,
    max: config.max,
    step: config.step,
    unit: config.unit,
    applyMode: writable ? 'immediate' : 'unavailable',
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
    capacity: { applied: null, min: 0, max: 0, step: 1, unit: 'batches' },
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

function channelCapacity(
  value: number,
  config: LoadgenConfigSnapshot['readerChannelCapacity'],
): ChannelTelemetrySnapshot['capacity'] {
  return {
    applied: value,
    min: config.allowed[0]!,
    max: config.allowed.at(-1)!,
    step: 1,
    unit: config.unit,
  }
}

function readerChannel(
  wire: WireSnapshot | null,
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'reader-to-throttler',
    'reader',
    'throttler',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: channelCapacity(
      wire.readerChannel.capacity,
      wire.config.readerChannelCapacity,
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
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'throttler-to-sender',
    'throttler',
    'sender',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: channelCapacity(
      wire.senderChannel.capacity,
      wire.config.senderChannelCapacity,
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
  const writable = connectionState === 'connected' && runState === 'running'

  return deepFreeze({
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.run.elapsedMs ?? 0,
    totalTransactions: wire?.run.totalTransactions ?? 0,
    config: wire?.config ?? null,
    reader: {
      id: 'reader',
      workers: senderControl(wire?.reader.workers ?? null, wire?.config.readerWorkers ?? null, writable),
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
        wire?.config.readerReadBatchSize ?? null,
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
        wire?.config.throttlerRequestedTps ?? null,
        writable,
      ),
      installed: {
        applied: wire?.throttler.installed ?? null,
        pending: null,
        applyMode: writable ? 'immediate' : 'unavailable',
        writable,
        unavailableReason: writable ? null : 'Unavailable in HTTP mode',
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: readerChannel(wire),
    senderChannel: senderChannel(wire),
    sender: {
      id: 'sender',
      workers: senderControl(wire?.sender.workers ?? null, wire?.config.senderWorkers ?? null, writable),
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
  config: RangeControlConfigSnapshot,
): value is number {
  return isWireInteger(value) &&
    value >= config.min &&
    value <= config.max &&
    (value - config.min) % config.step === 0
}

function throttlerRequestedTpsControl(
  value: number | null,
  config: LoadgenConfigSnapshot['throttlerRequestedTps'] | null,
  writable: boolean,
): NumericControlSnapshot {
  if (config === null) return unavailableControl('transactions/s')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: config.min,
    max: config.max,
    step: config.step,
    unit: config.unit,
    applyMode: writable ? 'immediate' : 'unavailable',
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

function decodeRangeControlConfig(
  value: unknown,
  name: string,
  { unit, mutability, min, max }: Pick<RangeControlConfigSnapshot, 'unit' | 'mutability' | 'min'> & { max?: number },
): RangeControlConfigSnapshot {
  if (!isExactObject(value, RANGE_CONFIG_KEYS)) {
    throw new Error(`snapshot ${name} config is invalid`)
  }
  if (
    !isWireInteger(value.initial) ||
    !isWireInteger(value.min) || value.min < min ||
    !isWireInteger(value.max) || value.max < value.min ||
    (max !== undefined && value.max > max) ||
    !isWireInteger(value.step) || value.step === 0 ||
    value.unit !== unit || value.mutability !== mutability
  ) {
    throw new Error(`snapshot ${name} config is invalid`)
  }

  const config = {
    initial: value.initial,
    min: value.min,
    max: value.max,
    step: value.step,
    unit,
    mutability,
  }
  if (!isRangeValue(config.initial, config)) {
    throw new Error(`snapshot ${name} config initial is invalid`)
  }
  return Object.freeze(config)
}

function decodeAllowedControlConfig(
  value: unknown,
  name: string,
): AllowedControlConfigSnapshot {
  if (!isExactObject(value, ALLOWED_CONFIG_KEYS)) {
    throw new Error(`snapshot ${name} config is invalid`)
  }
  if (
    !Array.isArray(value.allowed) || value.allowed.length === 0 ||
    !value.allowed.every(isWireInteger) ||
    value.unit !== 'batches' || value.mutability !== 'startup-only'
  ) {
    throw new Error(`snapshot ${name} config is invalid`)
  }

  const allowed = Object.freeze([...value.allowed])
  if (
    allowed.some((entry, index) => index > 0 && entry <= allowed[index - 1]!) ||
    !isWireInteger(value.initial) || !allowed.includes(value.initial)
  ) {
    throw new Error(`snapshot ${name} config values are invalid`)
  }
  return Object.freeze({
    initial: value.initial,
    allowed,
    unit: value.unit,
    mutability: value.mutability,
  })
}

function decodeConfig(value: unknown): LoadgenConfigSnapshot {
  if (!isExactObject(value, CONFIG_KEYS)) {
    throw new Error('snapshot config must contain exactly reader, readerChannel, senderChannel, throttler, and metrics controls')
  }

  const readerReadBatchSize = decodeRangeControlConfig(value.readerReadBatchSize, 'reader', {
    unit: 'transactions', mutability: 'startup-only', min: 1,
  })
  const readerWorkers = decodeRangeControlConfig(value.readerWorkers, 'reader workers', {
    unit: 'workers', mutability: 'immediate', min: 1,
  })
  const metricsWindowMs = decodeRangeControlConfig(value.metricsWindowMs, 'metrics window', {
    unit: 'milliseconds', mutability: 'startup-only', min: 100, max: 10_000,
  })
  const readerChannelCapacity = decodeAllowedControlConfig(value.readerChannelCapacity, 'readerChannel')
  const senderChannelCapacity = decodeAllowedControlConfig(value.senderChannelCapacity, 'senderChannel')
  const throttlerRequestedTps = decodeRangeControlConfig(value.throttlerRequestedTps, 'throttler requested TPS', {
    unit: 'transactions/s', mutability: 'immediate', min: 0,
  })
  if (throttlerRequestedTps.max === throttlerRequestedTps.min) {
    throw new Error('snapshot throttler requested TPS config is invalid')
  }

  const installed = value.throttlerInstalled
  if (!isExactObject(installed, ['allowed', 'initial', 'mutability'])) {
    throw new Error('snapshot throttler installation mode config is invalid')
  }
  if (
    !Array.isArray(installed.allowed) ||
    installed.allowed.length !== 2 ||
    installed.allowed.some((entry) => typeof entry !== 'boolean') ||
    new Set(installed.allowed).size !== 2 ||
    typeof installed.initial !== 'boolean' ||
    !installed.allowed.includes(installed.initial) ||
    installed.mutability !== 'immediate'
  ) {
    throw new Error('snapshot throttler installation mode config is invalid')
  }

  const senderWorkers = decodeRangeControlConfig(value.senderWorkers, 'Sender', {
    unit: 'workers', mutability: 'immediate', min: 1,
  })
  const logging = value.logging
  if (
    !isExactObject(logging, ['level', 'mutability']) ||
    (logging.level !== 'debug' && logging.level !== 'info' &&
      logging.level !== 'warn' && logging.level !== 'error') ||
    logging.mutability !== 'startup-only'
  ) {
    throw new Error('snapshot logging config is invalid')
  }

  return Object.freeze({
    readerReadBatchSize,
    readerWorkers,
    metricsWindowMs,
    readerChannelCapacity,
    senderChannelCapacity,
    throttlerRequestedTps,
    throttlerInstalled: Object.freeze({
      initial: installed.initial,
      allowed: Object.freeze([...installed.allowed]),
      mutability: installed.mutability,
    }),
    senderWorkers,
    logging: Object.freeze({ level: logging.level, mutability: logging.mutability }),
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
  if (!isExactObject(value, WIRE_KEYS)) {
    throw new Error('snapshot body must contain exactly seven sections')
  }
  const { run, reader, throttler, sender } = value
  if (!isExactObject(run, RUN_KEYS)) throw new Error('snapshot run is invalid')
  if (!isExactObject(reader, READER_KEYS)) throw new Error('snapshot reader is invalid')
  if (!isExactObject(throttler, THROTTLER_KEYS)) throw new Error('snapshot throttler is invalid')
  if (!isExactObject(sender, SENDER_KEYS)) throw new Error('snapshot sender is invalid')
  const config = decodeConfig(value.config)

  const state = run.state
  if (state !== 'starting' && state !== 'running' && state !== 'stopping' && state !== 'faulted') {
    throw new Error('snapshot run state is invalid')
  }
  if (!isWireInteger(run.elapsedMs) || !isWireInteger(run.totalTransactions)) {
    throw new Error('snapshot run values are invalid')
  }

  if (
    !isRangeValue(reader.workers, config.readerWorkers!) ||
    !isRangeValue(reader.readBatchSize, config.readerReadBatchSize) ||
    !isWireNumber(reader.readTps) || !isWireInteger(reader.rowsRead) ||
    !isWireInteger(reader.liveWorkers) || !isWireInteger(reader.idleWorkers) ||
    !isWireInteger(reader.readingWorkers) || !isWireInteger(reader.blockedWorkers) ||
    !isWireInteger(reader.drainingWorkers) || !isWireInteger(reader.drainingIdleWorkers) ||
    !isWireInteger(reader.drainingReadingWorkers) || !isWireInteger(reader.drainingBlockedWorkers)
  ) {
    throw new Error('snapshot reader values are invalid')
  }
  if (
    reader.liveWorkers !== reader.idleWorkers + reader.readingWorkers + reader.blockedWorkers ||
    reader.drainingWorkers !== reader.drainingIdleWorkers + reader.drainingReadingWorkers + reader.drainingBlockedWorkers ||
    reader.drainingIdleWorkers > reader.idleWorkers ||
    reader.drainingReadingWorkers > reader.readingWorkers ||
    reader.drainingBlockedWorkers > reader.blockedWorkers ||
    (state === 'starting' && (reader.liveWorkers !== 0 || reader.drainingWorkers !== 0))
  ) {
    throw new Error('snapshot reader values are invalid')
  }
  if (typeof reader.sourceDirectory !== 'string' || reader.sourceDirectory.length === 0) {
    throw new Error('snapshot reader sourceDirectory is invalid')
  }

  const sourceError = reader.sourceError
  if (sourceError !== null) {
    if (!isExactObject(sourceError, SOURCE_ERROR_KEYS)) {
      throw new Error('snapshot reader sourceError is invalid')
    }
    const { operation, relativePath, message } = sourceError
    if (
      sourceError.category !== 'source' ||
      (operation !== 'glob' && operation !== 'open' && operation !== 'read' &&
        operation !== 'close' && operation !== 'reader-close') ||
      typeof relativePath !== 'string' || relativePath.length === 0 ||
      relativePath.startsWith('/') || relativePath.startsWith('../') ||
      typeof message !== 'string' || message.length === 0
    ) {
      throw new Error('snapshot reader sourceError is invalid')
    }
  }

  if (
    !isRangeValue(throttler.requestedTps, config.throttlerRequestedTps) ||
    !isWireNumber(throttler.admittedTps) ||
    typeof throttler.installed !== 'boolean' ||
    !config.throttlerInstalled.allowed.includes(throttler.installed)
  ) {
    throw new Error('snapshot throttler values are invalid')
  }

  if (
    !isRangeValue(sender.workers, config.senderWorkers) ||
    !isWireInteger(sender.liveWorkers) || !isWireInteger(sender.idleWorkers) ||
    !isWireInteger(sender.inFlightWorkers) || !isWireInteger(sender.backoffWorkers) ||
    !isWireInteger(sender.drainingWorkers) || !isWireInteger(sender.drainingIdleWorkers) ||
    !isWireInteger(sender.drainingInFlightWorkers) || !isWireInteger(sender.drainingBackoffWorkers)
  ) {
    throw new Error('snapshot sender values are invalid')
  }
  if (
    sender.liveWorkers !== sender.idleWorkers + sender.inFlightWorkers + sender.backoffWorkers ||
    sender.drainingWorkers !== sender.drainingIdleWorkers + sender.drainingInFlightWorkers + sender.drainingBackoffWorkers ||
    sender.drainingIdleWorkers > sender.idleWorkers ||
    sender.drainingInFlightWorkers > sender.inFlightWorkers ||
    sender.drainingBackoffWorkers > sender.backoffWorkers ||
    (state === 'starting' && (sender.liveWorkers !== 0 || sender.drainingWorkers !== 0))
  ) {
    throw new Error('snapshot sender values are invalid')
  }

  return {
    run: run as unknown as WireRun,
    reader: reader as unknown as WireReader,
    throttler: throttler as unknown as WireThrottler,
    sender: sender as unknown as WireSender,
    readerChannel: decodeChannelSnapshot(value.readerChannel, config.readerChannelCapacity.allowed),
    senderChannel: decodeChannelSnapshot(value.senderChannel, config.senderChannelCapacity.allowed),
    config,
  }
}

function readBatchSizeControl(
  value: number | null,
  config: LoadgenConfigSnapshot['readerReadBatchSize'] | null,
): NumericControlSnapshot {
  if (config === null) return unavailableControl('transactions')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: config.min,
    max: config.max,
    step: config.step,
    unit: config.unit,
    applyMode: 'unavailable',
  }
}

function canDispatchConfigCommand(
  command: SupportedCommand,
  snapshot: LoadgenTelemetrySnapshot,
): boolean {
  const config = snapshot.config
  if (snapshot.connectionState !== 'connected' || snapshot.runState !== 'running' || config === null) return false
  switch (command.type) {
    case 'set-requested-tps':
      return isRangeValue(command.value, config.throttlerRequestedTps)
    case 'set-throttler-installed':
      return config.throttlerInstalled.allowed.includes(command.value)
    case 'set-sender-workers':
      return isRangeValue(command.value, config.senderWorkers)
    case 'set-reader-workers':
      return config.readerWorkers !== undefined && isRangeValue(command.value, config.readerWorkers)
  }
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

    if (!canDispatchConfigCommand(command, this.snapshot)) {
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

    if (!canDispatchConfigCommand(command, this.snapshot)) {
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
        body: JSON.stringify({
          action: command.type,
          value: command.value,
        }),
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
        'command request failed due to a network error',
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
