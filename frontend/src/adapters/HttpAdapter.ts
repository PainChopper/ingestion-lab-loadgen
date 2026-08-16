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

const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const POLL_INTERVAL_MS = 1_000
const UNAVAILABLE_REASON = 'Недоступно в HTTP snapshot mode'
const UNAVAILABLE_COMMAND_MESSAGE = 'Команды недоступны в HTTP snapshot mode'
const WIRE_KEYS = Object.freeze([
  'readerWorkers',
  'runState',
  'senderWorkers',
  'totalTransactions',
])

interface WireSnapshot {
  readonly runState: RunState
  readonly totalTransactions: number
  readonly readerWorkers: number
  readonly senderWorkers: number
}

function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object' || Object.isFrozen(value)) {
    return value
  }

  for (const key of Reflect.ownKeys(value)) {
    deepFreeze((value as Record<PropertyKey, unknown>)[key])
  }
  return Object.freeze(value)
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
  if (value === null) return unavailableControl('workers')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: value,
    max: value,
    step: 1,
    unit: 'workers',
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
    elapsedMs: 0,
    totalTransactions: wire?.totalTransactions ?? 0,
    reader: {
      id: 'reader',
      workers: workerControl(wire?.readerWorkers ?? null),
      readBatchSize: unavailableControl('tx'),
      readTps: null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: null,
      source: null,
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
    queue1: neutralQueue(
      'reader-to-throttler',
      'reader',
      'throttler',
    ),
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
    throw new Error('snapshot body must contain exactly four wire keys')
  }

  if (
    record.runState !== 'idle' &&
    record.runState !== 'running' &&
    record.runState !== 'paused'
  ) {
    throw new Error('snapshot runState is invalid')
  }
  if (
    !isWireInteger(record.totalTransactions) ||
    !isWireInteger(record.readerWorkers) ||
    !isWireInteger(record.senderWorkers)
  ) {
    throw new Error('snapshot counters must be nonnegative safe integers')
  }

  return {
    runState: record.runState,
    totalTransactions: record.totalTransactions,
    readerWorkers: record.readerWorkers,
    senderWorkers: record.senderWorkers,
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
  private lastKnownWire: WireSnapshot | null = null
  private timer: ReturnType<typeof setInterval> | null
  private activeController: AbortController | null = null
  private requestInFlight = false
  private commandSequence = 0
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

  dispatch = async (command: LoadgenCommand): Promise<CommandReceipt> => {
    const commandId = `http-local-${++this.commandSequence}`

    return deepFreeze({
      commandId,
      commandType: command.type,
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: this.snapshot.revision,
      error: {
        code: 'unavailable',
        message: UNAVAILABLE_COMMAND_MESSAGE,
        retryable: false,
        details: null,
      },
    })
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

  private publish(snapshot: LoadgenTelemetrySnapshot): void {
    this.snapshot = snapshot
    this.listeners.forEach((listener) => listener(snapshot))
  }
}
