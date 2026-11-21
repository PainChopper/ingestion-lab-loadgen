import { z } from 'zod/mini'
import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  CommandReceipt,
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
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
    min: 0,
    max: 0,
    step: 1,
    unit,
    applyMode: 'unavailable',
  }
}

function numericControl(
  value: number | null,
  config:
    | WireSnapshot['config']['readerWorkers']
    | WireSnapshot['config']['throttlerRequestedTps']
    | null,
  applyMode: NumericControlSnapshot['applyMode'],
  unit: string,
): NumericControlSnapshot {
  if (config === null) return unavailableControl(unit)
  return {
    applied: value,
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
    depthBatches: null,
    blockedSenders: 0,
    oldestBlockedSenderMs: 0,
    inputTransactionsPerSecond: null,
    outputTransactionsPerSecond: null,
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
    inputTransactionsPerSecond: telemetry.inputTransactionsPerSecond,
    outputTransactionsPerSecond: telemetry.outputTransactionsPerSecond,
    blockedSenders: telemetry.blockedSenders,
    oldestBlockedSenderMs: telemetry.oldestBlockedSenderMs,
  }
}

function createSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: WireSnapshot | null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.run.state ?? null
  const writable = connectionState === 'connected' && runState === 'running'
  const applyMode = writable ? 'immediate' : 'unavailable'

  return deepFreeze({
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.run.elapsedMs ?? 0,
    totalTransactions: wire?.run.totalTransactions ?? 0,
    reader: {
      id: 'reader',
      workers: numericControl(
        wire?.reader.workers ?? null,
        wire?.config.readerWorkers ?? null,
        applyMode,
        'workers',
      ),
      workerStates: wire?.reader.workerStates ?? [],
      readBatchSize: wire?.reader.readBatchSize ?? null,
      readTps: wire?.reader.readTps ?? null,
      rowsRead: wire?.reader.rowsRead ?? null,
      sourceDirectory: wire?.reader.sourceDirectory,
      sourceError: wire?.reader.sourceError ?? null,
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
        applyMode,
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
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
      workerStates: wire?.sender.workerStates ?? [],
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
  category: z.string(),
  operation: z.string(),
  relativePath: z.string(),
  message: z.string(),
})

const channelSchema = z.object({
  capacity: wireInteger,
  depthBatches: wireInteger,
  bufferedTransactions: wireInteger,
  blockedSenders: wireInteger,
  oldestBlockedSenderMs: wireInteger,
  blockedMs: wireInteger,
  inputTransactionsPerSecond: wireNumber,
  outputTransactionsPerSecond: wireNumber,
})

const wireSnapshotSchema = z.object({
  run: z.object({
    state: z.enum(['starting', 'running', 'stopping', 'faulted']),
    elapsedMs: wireInteger,
    totalTransactions: wireInteger,
  }),
  reader: z.object({
    workers: wireInteger,
    workerStates: z.array(
      z.strictObject({
        activity: z.enum(['idle', 'reading', 'blocked']),
        draining: z.boolean(),
      }),
    ),
    readBatchSize: wireInteger.check(z.positive()),
    readTps: wireNumber,
    rowsRead: wireInteger,
    sourceDirectory: z.string(),
    sourceError: z.nullable(sourceErrorSchema),
  }),
  throttler: z.object({
    requestedTps: wireInteger,
    admittedTps: wireNumber,
    installed: z.boolean(),
  }),
  sender: z.object({
    workers: wireInteger,
    workerStates: z.array(
      z.strictObject({
        activity: z.enum(['idle', 'inFlight', 'backoff']),
        draining: z.boolean(),
      }),
    ),
  }),
  readerChannel: channelSchema,
  senderChannel: channelSchema,
  config: configSchema,
})

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
  config: WireSnapshot['config'] | null,
): boolean {
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
    if (!isSupportedCommand(command)) {
      return Promise.resolve(this.rejectCommand('unavailable', UNAVAILABLE_COMMAND_MESSAGE))
    }
    if (this.disposed) {
      return Promise.resolve(this.rejectCommand('disposed', DISPOSED_COMMAND_MESSAGE))
    }
    if (!canDispatchConfigCommand(command, this.snapshot, this.lastWire?.config ?? null)) {
      return Promise.resolve(this.rejectCommand('unavailable', UNAVAILABLE_COMMAND_MESSAGE))
    }

    const receipt = this.commandChannel.then(() => this.sendCommand(command))
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

  private sendCommand = async (command: SupportedCommand): Promise<CommandReceipt> => {
    if (this.disposed) {
      return this.rejectCommand('disposed', DISPOSED_COMMAND_MESSAGE)
    }

    if (!canDispatchConfigCommand(command, this.snapshot, this.lastWire?.config ?? null)) {
      return this.rejectCommand('unavailable', UNAVAILABLE_COMMAND_MESSAGE)
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
      return this.rejectCommand('disposed', DISPOSED_COMMAND_MESSAGE)
    }
    if (abortedBeforeCleanup || response === null) {
      return this.rejectCommand(
        'unavailable',
        abortedBeforeCleanup
          ? 'command request timed out'
          : 'command request failed due to a network error',
      )
    }

    if (isSuccessfulStatus(response.status)) {
      return deepFreeze({
        accepted: true,
        error: null,
      })
    }

    return this.rejectCommand(
      'unavailable',
      `command request failed with HTTP status ${response.status}`,
    )
  }

  private rejectCommand(code: 'disposed' | 'unavailable', message: string): CommandReceipt {
    return deepFreeze({
      accepted: false,
      error: {
        code,
        message,
      },
    })
  }

  private publish(snapshot: LoadgenTelemetrySnapshot): void {
    this.snapshot = snapshot
    this.listeners.forEach((listener) => listener(snapshot))
  }
}
