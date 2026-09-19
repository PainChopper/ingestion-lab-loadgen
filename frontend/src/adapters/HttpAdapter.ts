import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  CommandReceipt,
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  QueueTelemetrySnapshot,
  RunState,
} from '../model/loadgen'
import {
  isQueue1Capacity,
  QUEUE1_CAPACITY_VALUES,
} from '../model/queue1Capacity'

const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const COMMAND_ENDPOINT = '/api/loadgen/commands'
const POLL_INTERVAL_MS = 1_000
const UNAVAILABLE_REASON = 'Недоступно в HTTP snapshot mode'
const UNAVAILABLE_COMMAND_MESSAGE = 'command is not available in the HTTP adapter'
const DISPOSED_COMMAND_MESSAGE = 'http adapter is disposed'
const NETWORK_COMMAND_MESSAGE = 'command request failed due to a network error'
const WIRE_KEYS = Object.freeze([
  'elapsedMs',
  'queue1BlockedMs',
  'queue1BlockedSenders',
  'queue1Capacity',
  'queue1DepthBatches',
  'queue1DequeuedBatchesTotal',
  'queue1DequeuedTransactionsTotal',
  'queue1EnqueuedBatchesTotal',
  'queue1EnqueuedTransactionsTotal',
  'queue1InputBatchesPerSecond',
  'queue1InputTransactionsPerSecond',
  'queue1OldestBlockedSenderMs',
  'queue1OutputBatchesPerSecond',
  'queue1OutputTransactionsPerSecond',
  'queue1QueuedTransactions',
  'readerReadBatchSize',
  'readerReadTps',
  'readerRowsRead',
  'readerSource',
  'readerWorkers',
  'runState',
  'senderWorkers',
  'startError',
  'totalTransactions',
])

interface WireSnapshot {
  readonly runState: RunState
  readonly elapsedMs: number
  readonly startError: string | null
  readonly totalTransactions: number
  readonly readerWorkers: number
  readonly senderWorkers: number
  readonly readerReadTps: number
  readonly readerReadBatchSize: number
  readonly readerRowsRead: number
  readonly readerSource: string | null
  readonly queue1Capacity: number
  readonly queue1EnqueuedBatchesTotal: number
  readonly queue1EnqueuedTransactionsTotal: number
  readonly queue1DequeuedBatchesTotal: number
  readonly queue1DequeuedTransactionsTotal: number
  readonly queue1DepthBatches: number
  readonly queue1QueuedTransactions: number
  readonly queue1BlockedSenders: number
  readonly queue1OldestBlockedSenderMs: number
  readonly queue1BlockedMs: number
  readonly queue1InputBatchesPerSecond: number
  readonly queue1InputTransactionsPerSecond: number
  readonly queue1OutputBatchesPerSecond: number
  readonly queue1OutputTransactionsPerSecond: number
}

type SupportedCommand = Extract<
  LoadgenCommand,
  {
    type:
      | 'run'
      | 'pause'
      | 'reset'
      | 'set-queue-capacity'
      | 'set-read-batch-size'
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
    command.type === 'set-queue-capacity' ||
    command.type === 'set-read-batch-size'
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

function neutralQueue(
  id: QueueTelemetrySnapshot['id'],
  from: QueueTelemetrySnapshot['from'],
  to: QueueTelemetrySnapshot['to'],
): QueueTelemetrySnapshot {
  return {
    id,
    from,
    to,
    capacity: unavailableControl('batches'),
    enqueuedBatchesTotal: 0,
    enqueuedTransactionsTotal: 0,
    dequeuedBatchesTotal: 0,
    dequeuedTransactionsTotal: 0,
    depthBatches: null,
    queuedTransactions: null,
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

function queue1CapacityControl(
  value: number,
  connectionState: ConnectionState,
  runState: RunState,
): NumericControlSnapshot {
  return {
    applied: value,
    preview: null,
    pending: null,
    min: 0,
    max: QUEUE1_CAPACITY_VALUES.at(-1)!,
    step: 1,
    unit: 'batches',
    applyMode: connectionState === 'connected' && runState === 'idle'
      ? 'immediate'
      : 'unavailable',
  }
}

function queue1(
  wire: WireSnapshot | null,
  connectionState: ConnectionState,
  runState: RunState,
): QueueTelemetrySnapshot {
  const queue = neutralQueue(
    'reader-to-throttler',
    'reader',
    'throttler',
  )
  if (wire === null) return queue

  return {
    ...queue,
    capacity: queue1CapacityControl(
      wire.queue1Capacity,
      connectionState,
      runState,
    ),
    depthBatches: wire.queue1DepthBatches,
    queuedTransactions: wire.queue1QueuedTransactions,
    enqueuedBatchesTotal: wire.queue1EnqueuedBatchesTotal,
    enqueuedTransactionsTotal: wire.queue1EnqueuedTransactionsTotal,
    dequeuedBatchesTotal: wire.queue1DequeuedBatchesTotal,
    dequeuedTransactionsTotal: wire.queue1DequeuedTransactionsTotal,
    inputBatchesPerSecond: wire.queue1InputBatchesPerSecond,
    inputTransactionsPerSecond: wire.queue1InputTransactionsPerSecond,
    outputBatchesPerSecond: wire.queue1OutputBatchesPerSecond,
    outputTransactionsPerSecond: wire.queue1OutputTransactionsPerSecond,
    inputTps: wire.queue1InputTransactionsPerSecond,
    outputTps: wire.queue1OutputTransactionsPerSecond,
    throughputTps: wire.queue1OutputTransactionsPerSecond,
    blockedSenders: wire.queue1BlockedSenders,
    oldestBlockedSenderMs: wire.queue1OldestBlockedSenderMs,
    blockedMs: wire.queue1BlockedMs,
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
    reader: {
      id: 'reader',
      workers: workerControl(wire?.readerWorkers ?? null),
      readBatchSize: readBatchSizeControl(
        wire?.readerReadBatchSize ?? null,
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
      requestedTps: unavailableControl('tx/s'),
      installationMode: {
        applied: null,
        pending: null,
        applyMode: 'unavailable',
        writable: false,
        unavailableReason: UNAVAILABLE_REASON,
      },
      admittedTps: null,
      limitedMs: null,
      state: runState,
    },
    queue1: queue1(wire, connectionState, runState),
    queue2: neutralQueue(
      'throttler-to-sender',
      'throttler',
      'sender',
    ),
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

function isReadBatchSize(value: unknown): value is number {
  return isWireInteger(value) &&
    value >= 1_000 &&
    value <= 100_000 &&
    value % 1_000 === 0
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
    throw new Error('snapshot body must contain exactly twenty-four wire keys')
  }

  if (
    record.runState !== 'idle' &&
    record.runState !== 'running' &&
    record.runState !== 'paused'
  ) {
    throw new Error('snapshot runState is invalid')
  }
  if (
    !isWireInteger(record.elapsedMs) ||
    !isWireInteger(record.totalTransactions) ||
    !isWireInteger(record.readerWorkers) ||
    !isWireInteger(record.senderWorkers) ||
    !isWireInteger(record.readerRowsRead) ||
    !isQueue1Capacity(record.queue1Capacity) ||
    !isWireInteger(record.queue1EnqueuedBatchesTotal) ||
    !isWireInteger(record.queue1EnqueuedTransactionsTotal) ||
    !isWireInteger(record.queue1DequeuedBatchesTotal) ||
    !isWireInteger(record.queue1DequeuedTransactionsTotal) ||
    !isWireInteger(record.queue1DepthBatches) ||
    !isWireInteger(record.queue1QueuedTransactions) ||
    !isWireInteger(record.queue1BlockedSenders) ||
    !isWireInteger(record.queue1OldestBlockedSenderMs) ||
    !isWireInteger(record.queue1BlockedMs)
  ) {
    throw new Error('snapshot counters must be nonnegative safe integers')
  }
  if (!isWireNumber(record.readerReadTps)) {
    throw new Error('snapshot readerReadTps must be a nonnegative finite number')
  }
  if (
    !isWireNumber(record.queue1InputBatchesPerSecond) ||
    !isWireNumber(record.queue1InputTransactionsPerSecond) ||
    !isWireNumber(record.queue1OutputBatchesPerSecond) ||
    !isWireNumber(record.queue1OutputTransactionsPerSecond)
  ) {
    throw new Error('snapshot queue1 rates must be nonnegative finite numbers')
  }
  if (!isReadBatchSize(record.readerReadBatchSize)) {
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
    readerWorkers: record.readerWorkers,
    senderWorkers: record.senderWorkers,
    readerReadTps: record.readerReadTps,
    readerReadBatchSize: record.readerReadBatchSize,
    readerRowsRead: record.readerRowsRead,
    readerSource: record.readerSource,
    queue1Capacity: record.queue1Capacity,
    queue1EnqueuedBatchesTotal: record.queue1EnqueuedBatchesTotal,
    queue1EnqueuedTransactionsTotal: record.queue1EnqueuedTransactionsTotal,
    queue1DequeuedBatchesTotal: record.queue1DequeuedBatchesTotal,
    queue1DequeuedTransactionsTotal: record.queue1DequeuedTransactionsTotal,
    queue1DepthBatches: record.queue1DepthBatches,
    queue1QueuedTransactions: record.queue1QueuedTransactions,
    queue1BlockedSenders: record.queue1BlockedSenders,
    queue1OldestBlockedSenderMs: record.queue1OldestBlockedSenderMs,
    queue1BlockedMs: record.queue1BlockedMs,
    queue1InputBatchesPerSecond: record.queue1InputBatchesPerSecond,
    queue1InputTransactionsPerSecond: record.queue1InputTransactionsPerSecond,
    queue1OutputBatchesPerSecond: record.queue1OutputBatchesPerSecond,
    queue1OutputTransactionsPerSecond: record.queue1OutputTransactionsPerSecond,
  }
}

function readBatchSizeControl(
  value: number | null,
  connectionState: ConnectionState,
  runState: RunState,
): NumericControlSnapshot {
  return {
    applied: value,
    preview: null,
    pending: null,
    min: 1_000,
    max: 100_000,
    step: 1_000,
    unit: 'tx',
    applyMode: connectionState === 'connected' && runState === 'idle'
      ? 'immediate'
      : 'unavailable',
  }
}

function canDispatchQueue1Capacity(
  command: SupportedCommand,
  connectionState: ConnectionState,
  runState: RunState,
): boolean {
  return command.type !== 'set-queue-capacity' ||
    (command.queue === 'reader-to-throttler' &&
      isQueue1Capacity(command.value) &&
      connectionState === 'connected' &&
      runState === 'idle')
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
  private lastKnownWire: WireSnapshot | null = null
  private timer: ReturnType<typeof setInterval> | null
  private activeController: AbortController | null = null
  private requestInFlight = false
  private commandSequence = 0
  private commandQueue: Promise<void> = Promise.resolve()
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

    if (
      command.type === 'set-read-batch-size' &&
      (!isReadBatchSize(command.value) ||
        this.snapshot.connectionState !== 'connected' ||
        this.snapshot.runState !== 'idle')
    ) {
      return Promise.resolve(this.rejectCommand(
        commandId,
        command,
        'unavailable',
        UNAVAILABLE_COMMAND_MESSAGE,
        false,
      ))
    }

    if (!canDispatchQueue1Capacity(
      command,
      this.snapshot.connectionState,
      this.snapshot.runState,
    )) {
      return Promise.resolve(this.rejectCommand(
        commandId,
        command,
        'unavailable',
        UNAVAILABLE_COMMAND_MESSAGE,
        false,
      ))
    }

    const receipt = this.commandQueue.then(
      () => this.sendCommand(commandId, command),
    )
    this.commandQueue = receipt.then(
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
      this.publish(createSnapshot(revision, 'error', this.lastKnownWire))
      return
    }

    this.lastKnownWire = wire
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

    if (!canDispatchQueue1Capacity(
      command,
      this.snapshot.connectionState,
      this.snapshot.runState,
    )) {
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
            command.type === 'set-queue-capacity'
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
