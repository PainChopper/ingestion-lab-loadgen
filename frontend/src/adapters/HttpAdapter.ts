import { z } from 'zod/mini'
import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  CommandReceipt,
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  WorkersConfigSnapshot,
  TpsConfigSnapshot,
  ChannelTelemetrySnapshot,
} from '../model/loadgen'
const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const COMMAND_ENDPOINT = '/api/loadgen/commands'
const POLL_INTERVAL_MS = 1_000
const SNAPSHOT_TIMEOUT_MS = 2_000
const COMMAND_TIMEOUT_MS = 5_000
const UNAVAILABLE_COMMAND_MESSAGE = 'command is not available in the HTTP adapter'
const DISPOSED_COMMAND_MESSAGE = 'http adapter is disposed'
type SupportedCommand = Extract<
  LoadgenCommand,
  {
    type:
      'set-requested-tps' | 'set-throttler-installed' | 'set-reader-workers' | 'set-sender-workers'
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

function isSupportedCommand(command: LoadgenCommand): command is SupportedCommand {
  return (
    command.type === 'set-requested-tps' ||
    command.type === 'set-throttler-installed' ||
    command.type === 'set-reader-workers' ||
    command.type === 'set-sender-workers'
  )
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

function numericControl(
  value: number | null,
  config: WorkersConfigSnapshot | TpsConfigSnapshot | null,
  applyMode: NumericControlSnapshot['applyMode'],
  unit: string,
): NumericControlSnapshot {
  if (config === null) return unavailableControl(unit)
  return {
    applied: value,
    preview: null,
    pending: null,
    min: 'step' in config ? 0 : 1,
    max: config.max,
    step: 'step' in config ? config.step : 1,
    unit,
    applyMode,
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
    capacity: null,
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

function channelSnapshot(
  wire: WireSnapshot | null,
  side: 'reader' | 'sender',
): ChannelTelemetrySnapshot {
  const channel =
    side === 'reader'
      ? neutralChannel('reader-to-throttler', 'reader', 'throttler')
      : neutralChannel('throttler-to-sender', 'throttler', 'sender')
  if (wire === null) return channel

  const telemetry = wire[`${side}Channel`]
  return {
    ...channel,
    capacity: telemetry.capacity,
    depthBatches: telemetry.depthBatches,
    bufferedTransactions: telemetry.bufferedTransactions,
    sentBatchesTotal: telemetry.sentBatchesTotal,
    sentTransactionsTotal: telemetry.sentTransactionsTotal,
    receivedBatchesTotal: telemetry.receivedBatchesTotal,
    receivedTransactionsTotal: telemetry.receivedTransactionsTotal,
    inputBatchesPerSecond: telemetry.inputBatchesPerSecond,
    inputTransactionsPerSecond: telemetry.inputTransactionsPerSecond,
    outputBatchesPerSecond: telemetry.outputBatchesPerSecond,
    outputTransactionsPerSecond: telemetry.outputTransactionsPerSecond,
    inputTps: telemetry.inputTransactionsPerSecond,
    outputTps: telemetry.outputTransactionsPerSecond,
    throughputTps: telemetry.outputTransactionsPerSecond,
    blockedSenders: telemetry.blockedSenders,
    oldestBlockedSenderMs: telemetry.oldestBlockedSenderMs,
    blockedMs: telemetry.blockedMs,
  }
}

function createSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: WireSnapshot | null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.run.state ?? 'idle'
  const writable = connectionState === 'connected' && runState === 'running'
  const applyMode = writable ? 'immediate' : 'unavailable'

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
      workers: numericControl(
        wire?.reader.workers ?? null,
        wire?.config.readerWorkers ?? null,
        applyMode,
        'workers',
      ),
      liveWorkers: wire?.reader.liveWorkers ?? 0,
      drainingWorkers: wire?.reader.drainingWorkers ?? 0,
      idleWorkers: wire?.reader.idleWorkers ?? 0,
      readingWorkers: wire?.reader.readingWorkers ?? 0,
      blockedWorkers: wire?.reader.blockedWorkers ?? 0,
      drainingIdleWorkers: wire?.reader.drainingIdleWorkers ?? 0,
      drainingReadingWorkers: wire?.reader.drainingReadingWorkers ?? 0,
      drainingBlockedWorkers: wire?.reader.drainingBlockedWorkers ?? 0,
      readBatchSize: wire?.reader.readBatchSize ?? null,
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
      requestedTps: numericControl(
        wire?.throttler.requestedTps ?? null,
        wire?.config.throttlerRequestedTps ?? null,
        applyMode,
        'transactions/s',
      ),
      installed: {
        applied: wire?.throttler.installed ?? null,
        pending: null,
        applyMode,
        writable,
        unavailableReason: writable ? null : 'Unavailable in HTTP mode',
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: channelSnapshot(wire, 'reader'),
    senderChannel: channelSnapshot(wire, 'sender'),
    sender: {
      id: 'sender',
      workers: numericControl(
        wire?.sender.workers ?? null,
        wire?.config.senderWorkers ?? null,
        applyMode,
        'workers',
      ),
      liveWorkers: wire?.sender.liveWorkers ?? 0,
      drainingWorkers: wire?.sender.drainingWorkers ?? 0,
      timeoutMs: null,
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
  return (
    typeof value === 'number' && Number.isFinite(value) && Number.isSafeInteger(value) && value >= 0
  )
}

function isRangeValue(
  value: unknown,
  config: { min: number; max: number; step: number },
): value is number {
  return (
    isWireInteger(value) &&
    value >= config.min &&
    value <= config.max &&
    (value - config.min) % config.step === 0
  )
}

const wireInteger = z.int().check(z.nonnegative())
const wireNumber = z.number().check(z.nonnegative())
const nonemptyString = z.string().check(z.minLength(1))

const workersConfigSchema = z
  .strictObject({
    initial: wireInteger.check(z.positive()),
    max: wireInteger.check(z.positive()),
  })
  .check(
    z.refine(({ initial, max }) => initial <= max, {
      path: ['initial'],
      error: 'Expected initial within 1..max',
    }),
  )

const tpsConfigSchema = z
  .strictObject({
    initial: wireInteger,
    max: wireInteger.check(z.positive()),
    step: wireInteger.check(z.positive()),
  })
  .check(
    z.refine(({ initial, max, step }) => initial <= max && initial % step === 0, {
      path: ['initial'],
      error: 'Expected initial within 0..max and on the step grid',
    }),
  )

const configSchema = z.strictObject({
  readerWorkers: workersConfigSchema,
  senderWorkers: workersConfigSchema,
  throttlerRequestedTps: tpsConfigSchema,
})

const sourceErrorSchema = z.object({
  category: z.literal('source'),
  operation: z.enum(['glob', 'open', 'read', 'close', 'reader-close']),
  relativePath: nonemptyString.check(
    z.refine((path) => !path.startsWith('/') && !path.startsWith('../'), {
      error: 'Expected a relative source path',
    }),
  ),
  message: nonemptyString,
})

const channelSchema = z.object({
  capacity: wireInteger,
  depthBatches: wireInteger,
  bufferedTransactions: wireInteger,
  blockedSenders: wireInteger,
  oldestBlockedSenderMs: wireInteger,
  blockedMs: wireInteger,
  sentBatchesTotal: wireInteger,
  sentTransactionsTotal: wireInteger,
  receivedBatchesTotal: wireInteger,
  receivedTransactionsTotal: wireInteger,
  inputBatchesPerSecond: wireNumber,
  inputTransactionsPerSecond: wireNumber,
  outputBatchesPerSecond: wireNumber,
  outputTransactionsPerSecond: wireNumber,
})

const wireSnapshotSchema = z
  .object({
    run: z.object({
      state: z.enum(['starting', 'running', 'stopping', 'faulted']),
      elapsedMs: wireInteger,
      totalTransactions: wireInteger,
    }),
    reader: z.object({
      workers: wireInteger,
      liveWorkers: wireInteger,
      idleWorkers: wireInteger,
      readingWorkers: wireInteger,
      blockedWorkers: wireInteger,
      drainingWorkers: wireInteger,
      drainingIdleWorkers: wireInteger,
      drainingReadingWorkers: wireInteger,
      drainingBlockedWorkers: wireInteger,
      readBatchSize: wireInteger.check(z.positive()),
      readTps: wireNumber,
      rowsRead: wireInteger,
      sourceDirectory: nonemptyString,
      sourceError: z.nullable(sourceErrorSchema),
    }),
    throttler: z.object({
      requestedTps: wireInteger,
      admittedTps: wireNumber,
      installed: z.boolean(),
    }),
    sender: z.object({
      workers: wireInteger,
      liveWorkers: wireInteger,
      idleWorkers: wireInteger,
      inFlightWorkers: wireInteger,
      backoffWorkers: wireInteger,
      drainingWorkers: wireInteger,
      drainingIdleWorkers: wireInteger,
      drainingInFlightWorkers: wireInteger,
      drainingBackoffWorkers: wireInteger,
    }),
    readerChannel: channelSchema,
    senderChannel: channelSchema,
    config: configSchema,
  })
  .check(
    z.superRefine((wire, ctx) => {
      const { run, reader, sender, throttler, config } = wire
      const starting = run.state === 'starting'
      for (const [path, value, range] of [
        [['reader', 'workers'], reader.workers, { min: 1, max: config.readerWorkers.max, step: 1 }],
        [
          ['throttler', 'requestedTps'],
          throttler.requestedTps,
          { min: 0, ...config.throttlerRequestedTps },
        ],
        [['sender', 'workers'], sender.workers, { min: 1, max: config.senderWorkers.max, step: 1 }],
      ] as const) {
        if (!isRangeValue(value, range)) {
          ctx.addIssue({
            code: 'custom',
            path: [...path],
            message: 'Value is outside the configured range or step grid',
          })
        }
      }
      if (
        reader.liveWorkers !== reader.idleWorkers + reader.readingWorkers + reader.blockedWorkers ||
        reader.drainingWorkers !==
          reader.drainingIdleWorkers +
            reader.drainingReadingWorkers +
            reader.drainingBlockedWorkers ||
        reader.drainingIdleWorkers > reader.idleWorkers ||
        reader.drainingReadingWorkers > reader.readingWorkers ||
        reader.drainingBlockedWorkers > reader.blockedWorkers ||
        (starting && (reader.liveWorkers !== 0 || reader.drainingWorkers !== 0))
      ) {
        ctx.addIssue({ code: 'custom', path: ['reader'], message: 'Inconsistent worker counts' })
      }
      if (
        sender.liveWorkers !==
          sender.idleWorkers + sender.inFlightWorkers + sender.backoffWorkers ||
        sender.drainingWorkers !==
          sender.drainingIdleWorkers +
            sender.drainingInFlightWorkers +
            sender.drainingBackoffWorkers ||
        sender.drainingIdleWorkers > sender.idleWorkers ||
        sender.drainingInFlightWorkers > sender.inFlightWorkers ||
        sender.drainingBackoffWorkers > sender.backoffWorkers ||
        (starting && (sender.liveWorkers !== 0 || sender.drainingWorkers !== 0))
      ) {
        ctx.addIssue({ code: 'custom', path: ['sender'], message: 'Inconsistent worker counts' })
      }
    }),
  )

type WireSnapshot = z.infer<typeof wireSnapshotSchema>

function appendFieldPath(path: string, key: PropertyKey): string {
  if (typeof key === 'string' && /^[a-z_$][\w$]*$/i.test(key)) {
    return path === '' ? key : `${path}.${key}`
  }
  return `${path}[${JSON.stringify(key)}]`
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

// Zod strips unknown keys. Compare only the decoded branches to report what it discarded.
function collectUnknownPaths(input: unknown, parsed: unknown, paths: string[], prefix = ''): void {
  if (!isObject(input) || !isObject(parsed)) return

  for (const [key, value] of Object.entries(input)) {
    const path = appendFieldPath(prefix, key)
    if (Object.hasOwn(parsed, key)) {
      collectUnknownPaths(value, parsed[key], paths, path)
    } else {
      paths.push(path)
    }
  }
}

function canDispatchConfigCommand(
  command: SupportedCommand,
  snapshot: LoadgenTelemetrySnapshot,
): boolean {
  const config = snapshot.config
  if (
    snapshot.connectionState !== 'connected' ||
    snapshot.runState !== 'running' ||
    config === null
  )
    return false
  switch (command.type) {
    case 'set-requested-tps':
      return isRangeValue(command.value, { min: 0, ...config.throttlerRequestedTps })
    case 'set-throttler-installed':
      return typeof command.value === 'boolean'
    case 'set-sender-workers':
      return isRangeValue(command.value, { min: 1, max: config.senderWorkers.max, step: 1 })
    case 'set-reader-workers':
      return isRangeValue(command.value, { min: 1, max: config.readerWorkers.max, step: 1 })
  }
}

function startRequest(timeoutMs: number) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  const finish = () => clearTimeout(timer)
  return {
    signal: controller.signal,
    finish,
    abort: () => {
      finish()
      controller.abort()
    },
  }
}

type ActiveRequest = ReturnType<typeof startRequest>

function isSuccessfulStatus(status: number): boolean {
  return status >= 200 && status <= 299
}

async function decodeResponse(response: Response) {
  const mediaType = response.headers.get('Content-Type')?.split(';', 1)[0].trim().toLowerCase()
  if (mediaType !== 'application/json') {
    throw new Error('snapshot response must be JSON')
  }

  const body: unknown = await response.json()
  const parsed = wireSnapshotSchema.safeParse(body)
  if (!parsed.success) {
    const issue = parsed.error.issues[0]!
    const path = issue.path.reduce<string>(appendFieldPath, '')
    throw new Error(`snapshot ${path || 'body'}: ${issue.message}`)
  }

  const unknownPaths: string[] = []
  collectUnknownPaths(body, parsed.data, unknownPaths)
  return { wire: parsed.data, unknownPaths }
}

export class HttpAdapter implements LoadgenAdapter {
  readonly kind = 'http' as const

  private readonly listeners = new Set<LoadgenSnapshotListener>()
  private readonly warnedUnknownPathSets = new Set<string>()
  private snapshot: LoadgenTelemetrySnapshot = createSnapshot(0, 'connecting', null)
  private timer: ReturnType<typeof setInterval> | null
  private activePoll: ActiveRequest | null = null
  private activeCommand: ActiveRequest | null = null
  private lastWire: WireSnapshot | null = null
  private lastDecodeError: string | null = null
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
      return Promise.resolve(
        this.rejectCommand(commandId, command, 'unavailable', UNAVAILABLE_COMMAND_MESSAGE, false),
      )
    }

    if (this.disposed) {
      return Promise.resolve(
        this.rejectCommand(commandId, command, 'disposed', DISPOSED_COMMAND_MESSAGE, false),
      )
    }

    if (!canDispatchConfigCommand(command, this.snapshot)) {
      return Promise.resolve(
        this.rejectCommand(commandId, command, 'unavailable', UNAVAILABLE_COMMAND_MESSAGE, false),
      )
    }

    const receipt = this.commandChannel.then(() => this.sendCommand(commandId, command))
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
    this.activePoll?.abort()
    this.activeCommand?.abort()
    this.listeners.clear()
  }

  private poll = async (): Promise<void> => {
    if (this.disposed || this.activePoll !== null) return

    const request = startRequest(SNAPSHOT_TIMEOUT_MS)
    this.activePoll = request
    let decoded: Awaited<ReturnType<typeof decodeResponse>> | null = null
    let decoding = false

    try {
      const response = await fetch(SNAPSHOT_ENDPOINT, {
        method: 'GET',
        headers: { Accept: 'application/json' },
        signal: request.signal,
      })
      if (!request.signal.aborted && isSuccessfulStatus(response.status)) {
        decoding = true
        decoded = await decodeResponse(response)
      }
    } catch (error) {
      if (decoding && !request.signal.aborted) {
        const message = error instanceof Error ? error.message : String(error)
        if (message !== this.lastDecodeError) {
          this.lastDecodeError = message
          console.warn(`LoadGen snapshot decoding failed: ${message}`)
        }
      }
    } finally {
      if (decoded === null) {
        request.abort()
      } else {
        request.finish()
      }
      this.activePoll = null
    }

    if (this.disposed) return

    const revision = this.snapshot.revision + 1
    if (decoded === null || request.signal.aborted) {
      this.publish(createSnapshot(revision, 'error', this.lastWire))
      return
    }

    const { wire, unknownPaths } = decoded
    this.lastWire = wire
    this.lastDecodeError = null
    if (unknownPaths.length !== 0) {
      const key = JSON.stringify(unknownPaths.sort())
      if (!this.warnedUnknownPathSets.has(key)) {
        this.warnedUnknownPathSets.add(key)
        console.warn(`LoadGen snapshot unknown fields: ${unknownPaths.join(', ')}`)
      }
    }
    this.publish(createSnapshot(revision, 'connected', wire))
  }

  private sendCommand = async (
    commandId: string,
    command: SupportedCommand,
  ): Promise<CommandReceipt> => {
    if (this.disposed) {
      return this.rejectCommand(commandId, command, 'disposed', DISPOSED_COMMAND_MESSAGE, false)
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

    const request = startRequest(COMMAND_TIMEOUT_MS)
    this.activeCommand = request
    let response: Response | null
    let abortedBeforeCleanup: boolean
    try {
      response = await fetch(COMMAND_ENDPOINT, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: command.type,
          value: command.value,
        }),
        signal: request.signal,
      })
    } catch {
      response = null
    } finally {
      // Abort releases the unread response body even after the headers have arrived.
      abortedBeforeCleanup = request.signal.aborted
      request.abort()
      this.activeCommand = null
    }

    if (this.disposed) {
      return this.rejectCommand(commandId, command, 'disposed', DISPOSED_COMMAND_MESSAGE, false)
    }
    if (abortedBeforeCleanup || response === null) {
      return this.rejectCommand(
        commandId,
        command,
        'unavailable',
        abortedBeforeCleanup
          ? 'command request timed out'
          : 'command request failed due to a network error',
        true,
      )
    }

    if (isSuccessfulStatus(response.status)) {
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
