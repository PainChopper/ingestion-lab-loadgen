import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  AdapterError,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
} from '../model/loadgen'
import {
  FIXED_STEP_MS,
  FixedStepSimulation,
  RETRY_BACKOFF_BASE_MS,
  RETRY_BACKOFF_MULTIPLIER,
  RETRY_JITTER_PERCENT,
  RETRY_MAX_ATTEMPTS,
  RETRYABLE_STATUS_CODES,
  SNAPSHOT_INTERVAL_MS,
} from '../model/simulation'
import type { ChannelTelemetry, SimulationConfig, SimulationTelemetry } from '../model/simulation'

const TARGET_ENDPOINT = 'http://target:8080/ingest'
const RETRY_POLICY = Object.freeze({
  maxAttempts: RETRY_MAX_ATTEMPTS,
  backoffBaseMs: RETRY_BACKOFF_BASE_MS,
  backoffMultiplier: RETRY_BACKOFF_MULTIPLIER,
  jitterPercent: RETRY_JITTER_PERCENT,
  retryableStatusCodes: Object.freeze([...RETRYABLE_STATUS_CODES]),
  retryTimeouts: true,
})

const RANGES = Object.freeze({
  readerWorkers: Object.freeze({ min: 1, max: 7, step: 1 }),
  senderWorkers: Object.freeze({ min: 1, max: 32, step: 1 }),
  requestedTps: Object.freeze({ min: 0, max: 250_000, step: 5_000 }),
})

interface NumericRange {
  readonly min: number
  readonly max: number
  readonly step: number
}

function numericControl(
  applied: number,
  range: NumericRange,
  unit: string,
  applyMode: NumericControlSnapshot['applyMode'] = 'immediate',
): NumericControlSnapshot {
  return Object.freeze({
    applied,
    preview: null,
    pending: null,
    min: range.min,
    max: range.max,
    step: range.step,
    unit,
    applyMode,
  })
}

function normalizeNumericValue(value: number, range: NumericRange): number {
  const bounded = Math.min(range.max, Math.max(range.min, value))
  const stepped = range.min + Math.round((bounded - range.min) / range.step) * range.step
  return Math.min(range.max, Math.max(range.min, stepped))
}

function freezeChannelSnapshot(
  channel: ChannelTelemetry,
  identity: {
    readonly id: 'reader-to-throttler' | 'throttler-to-sender'
    readonly from: 'reader' | 'throttler'
    readonly to: 'throttler' | 'sender'
  },
) {
  return Object.freeze({
    id: identity.id,
    from: identity.from,
    to: identity.to,
    capacity: channel.capacity,
    sentBatchesTotal: channel.sentBatchesTotal,
    sentTransactionsTotal: channel.sentTransactionsTotal,
    receivedBatchesTotal: channel.receivedBatchesTotal,
    receivedTransactionsTotal: channel.receivedTransactionsTotal,
    depthBatches: channel.depthBatches,
    bufferedTransactions: channel.bufferedTransactions,
    handoffBatches: channel.handoffBatches,
    handoffBatchesTotal: channel.handoffBatchesTotal,
    blockedSenders: channel.blockedSenders,
    oldestBlockedSenderMs: channel.oldestBlockedSenderMs,
    inputBatchesPerSecond: Math.round(channel.inputBatchesPerSecond),
    outputBatchesPerSecond: Math.round(channel.outputBatchesPerSecond),
    inputTransactionsPerSecond: Math.round(channel.inputTransactionsPerSecond),
    outputTransactionsPerSecond: Math.round(channel.outputTransactionsPerSecond),
    inputTps: Math.round(channel.inputTransactionsPerSecond),
    outputTps: Math.round(channel.outputTransactionsPerSecond),
    throughputTps: Math.round(channel.outputTransactionsPerSecond),
    blockedMs: Math.floor(channel.blockedMsTotal),
    trend: channel.trend,
  })
}

function freezeSnapshot(
  revision: number,
  simulation: FixedStepSimulation,
): LoadgenTelemetrySnapshot {
  const telemetry: SimulationTelemetry = simulation.telemetry()
  const config = simulation.config
  const readerCapacityTps = Math.round(telemetry.readerCapacityTps)
  const readerReadTps = Math.round(telemetry.readerTransactionsPerSecond)
  const senderWorkerStates = telemetry.sender.workerStates
  const drainingSenderWorkerStates = telemetry.sender.drainingWorkerStates
  const drainingWorkers =
    drainingSenderWorkerStates.idle +
    drainingSenderWorkerStates.inFlight +
    drainingSenderWorkerStates.backoff
  const readerLimitationReason =
    telemetry.readerChannel.blockedSenders > 0 && readerReadTps < readerCapacityTps
      ? 'downstream-backpressure'
      : null

  return Object.freeze({
    revision,
    adapterKind: 'simulation',
    connectionState: 'connected',
    runState: 'running',
    elapsedMs: telemetry.elapsedMs,
    totalTransactions: telemetry.totalTransactions,
    config: null,
    reader: Object.freeze({
      id: 'reader',
      workers: numericControl(config.readerWorkers, RANGES.readerWorkers, 'workers'),
      liveWorkers: config.readerWorkers,
      idleWorkers: readerReadTps === 0 ? config.readerWorkers : 0,
      readingWorkers: readerReadTps > 0 ? config.readerWorkers : 0,
      blockedWorkers: 0,
      drainingWorkers: 0,
      drainingIdleWorkers: 0,
      drainingReadingWorkers: 0,
      drainingBlockedWorkers: 0,
      readBatchSize: config.readBatchSize,
      readTps: readerReadTps,
      configuredCapacityTps: readerCapacityTps,
      limitationReason: readerLimitationReason,
      rowsRead: telemetry.readerChannel.sentTransactionsTotal,
      sourceError: null,
      state: 'running',
    }),
    throttler: Object.freeze({
      id: 'throttler',
      requestedTps: numericControl(config.requestedTps, RANGES.requestedTps, 'tx/s'),
      installed: Object.freeze({
        applied: config.throttlerInstalled,
        pending: null,
        applyMode: 'immediate',
        writable: true,
        unavailableReason: null,
      }),
      admittedTps: Math.round(telemetry.admittedTransactionsPerSecond),
      limitedMs: Math.floor(telemetry.limitedMs),
      state: 'running',
    }),
    readerChannel: freezeChannelSnapshot(telemetry.readerChannel, {
      id: 'reader-to-throttler',
      from: 'reader',
      to: 'throttler',
    }),
    senderChannel: freezeChannelSnapshot(telemetry.senderChannel, {
      id: 'throttler-to-sender',
      from: 'throttler',
      to: 'sender',
    }),
    sender: Object.freeze({
      id: 'sender',
      workers: numericControl(config.senderWorkers, RANGES.senderWorkers, 'workers'),
      liveWorkers: telemetry.sender.liveWorkers,
      idleWorkers: senderWorkerStates.idle + drainingSenderWorkerStates.idle,
      inFlightWorkers: senderWorkerStates.inFlight + drainingSenderWorkerStates.inFlight,
      backoffWorkers: senderWorkerStates.backoff + drainingSenderWorkerStates.backoff,
      drainingWorkers: drainingWorkers,
      drainingIdleWorkers: drainingSenderWorkerStates.idle,
      drainingInFlightWorkers: drainingSenderWorkerStates.inFlight,
      drainingBackoffWorkers: drainingSenderWorkerStates.backoff,
      timeoutMs: config.httpTimeoutMs,
      retryPolicy: RETRY_POLICY,
      attemptedTps: Math.round(telemetry.attemptedTransactionsPerSecond),
      retryAttemptedTps: Math.round(telemetry.sender.retryAttemptedTransactionsPerSecond),
      terminalFailedTps: Math.round(telemetry.sender.terminalFailedTransactionsPerSecond),
      inFlightRequests: telemetry.http.inFlightRequests,
      attemptsStartedTotal: telemetry.http.requestsStartedTotal,
      retryAttemptsStartedTotal: telemetry.sender.retryAttemptsStartedTotal,
      successfulResponses: telemetry.http.requestsSucceededTotal,
      failedResponses: telemetry.http.responsesRejectedTotal,
      retries: telemetry.sender.retryAttemptsStartedTotal,
      timeoutsTotal: telemetry.http.requestsTimedOutTotal,
      terminalFailedBatchesTotal: telemetry.sender.terminalFailedBatchesTotal,
      terminalFailedTransactionsTotal: telemetry.sender.terminalFailedTransactionsTotal,
      ambiguousTimeoutTransactionsTotal: telemetry.sender.ambiguousTimeoutTransactionsTotal,
      duplicateRiskTransactionsTotal: telemetry.sender.duplicateRiskTransactionsTotal,
      ambiguousTerminalTransactionsTotal: telemetry.sender.ambiguousTerminalTransactionsTotal,
      state: 'running',
    }),
    http: Object.freeze({
      id: 'http',
      connectionState: 'connected',
      statusCode: telemetry.http.latestStatusCode,
      lastOutcome: telemetry.http.lastOutcome,
      throughputTps: Math.round(telemetry.http.startedTransactionsPerSecond),
      inFlightRequests: telemetry.http.inFlightRequests,
      requestsStartedTotal: telemetry.http.requestsStartedTotal,
      requestsCompletedTotal: telemetry.http.requestsCompletedTotal,
      requestsSucceededTotal: telemetry.http.requestsSucceededTotal,
      requestsFailedTotal: telemetry.http.requestsFailedTotal,
      requestsTimedOutTotal: telemetry.http.requestsTimedOutTotal,
      networkErrorsTotal: telemetry.http.networkErrorsTotal,
      latencyP95Ms: telemetry.http.latestLatencyMs,
    }),
    target: Object.freeze({
      id: 'target',
      endpoint: TARGET_ENDPOINT,
      acceptedTps: Math.round(telemetry.acceptedTransactionsPerSecond),
      rejectedTps: Math.round(telemetry.rejectedTransactionsPerSecond),
      latencyP95Ms: telemetry.http.latestResponseLatencyMs,
      http200Responses: telemetry.http.requestsSucceededTotal,
      http503Responses: simulation.http503ResponsesTotal,
      connectionState: 'connected',
    }),
  })
}

export class SimulationAdapter implements LoadgenAdapter {
  readonly kind = 'simulation' as const

  private readonly listeners = new Set<LoadgenSnapshotListener>()
  private readonly simulation = new FixedStepSimulation(
    {
      readerWorkers: 5,
      senderWorkers: 3,
      requestedTps: 120_000,
      readBatchSize: 1_000,
      httpTimeoutMs: 500,
      targetDelayMs: 100,
      targetErrorRatePercent: 2,
    },
    4,
    100,
  )
  private snapshot: LoadgenTelemetrySnapshot
  private timer: ReturnType<typeof setInterval> | null
  private lastTickMs: number
  private pendingStepMs = 0
  private commandSequence = 0
  private disposed = false

  constructor() {
    this.snapshot = freezeSnapshot(0, this.simulation)
    this.lastTickMs = Date.now()
    this.timer = setInterval(this.tick, SNAPSHOT_INTERVAL_MS)
  }

  getSnapshot = (): LoadgenTelemetrySnapshot => this.snapshot

  subscribe = (listener: LoadgenSnapshotListener): (() => void) => {
    if (this.disposed) return () => undefined

    this.listeners.add(listener)
    listener(this.snapshot)
    return () => this.listeners.delete(listener)
  }

  dispatch = async (command: LoadgenCommand): Promise<CommandReceipt> => {
    const commandId = `simulation-${++this.commandSequence}`

    if (this.disposed) {
      return this.reject(commandId, command, {
        code: 'disposed',
        message: 'simulation adapter is disposed',
        retryable: false,
        details: null,
      })
    }

    let changed = false

    switch (command.type) {
      case 'set-requested-tps':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'requested tps')
        }
        changed = this.updateConfig(
          'requestedTps',
          normalizeNumericValue(command.value, RANGES.requestedTps),
        )
        break
      case 'set-reader-workers':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'worker count')
        }
        changed = this.updateConfig(
          'readerWorkers',
          normalizeNumericValue(command.value, RANGES.readerWorkers),
        )
        break
      case 'set-sender-workers':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'sender workers')
        }
        changed = this.updateConfig(
          'senderWorkers',
          normalizeNumericValue(command.value, RANGES.senderWorkers),
        )
        break
      case 'set-throttler-installed':
        changed = this.updateConfig('throttlerInstalled', command.value)
        break
      default:
        return this.reject(commandId, command, {
          code: 'unavailable',
          message: 'command is not available in the simulation adapter',
          retryable: false,
          details: null,
        })
    }

    if (changed) this.publish()

    return Object.freeze({
      commandId,
      commandType: command.type,
      accepted: true,
      applyMode: 'immediate',
      appliedAtMs: Date.now(),
      snapshotRevision: this.snapshot.revision,
      error: null,
    })
  }

  dispose = (): void => {
    if (this.disposed) return
    this.disposed = true
    if (this.timer !== null) {
      clearInterval(this.timer)
      this.timer = null
    }
    this.listeners.clear()
  }

  private tick = (): void => {
    if (this.disposed) return
    if (this.advanceToNow()) this.publish()
  }

  private advanceToNow(): boolean {
    const now = Date.now()
    const deltaMs = Math.max(0, now - this.lastTickMs)
    this.lastTickMs = now
    if (deltaMs === 0) return false

    this.pendingStepMs += deltaMs
    const steps = Math.floor(this.pendingStepMs / FIXED_STEP_MS)
    for (let index = 0; index < steps; index += 1) {
      this.simulation.advanceStep()
    }
    this.pendingStepMs -= steps * FIXED_STEP_MS
    return steps > 0
  }

  private updateConfig<
    Key extends 'readerWorkers' | 'senderWorkers' | 'requestedTps' | 'throttlerInstalled',
  >(key: Key, value: SimulationConfig[Key]): boolean {
    const advanced = this.advanceToNow()
    if (this.simulation.config[key] === value) return advanced
    this.simulation.updateConfig({ [key]: value })
    return true
  }

  private rejectInvalidNumber(
    commandId: string,
    command: LoadgenCommand,
    field: string,
  ): CommandReceipt {
    return this.reject(commandId, command, {
      code: 'invalid-command',
      message: `${field} must be a finite number`,
      retryable: false,
      details: null,
    })
  }

  private publish(): void {
    this.snapshot = freezeSnapshot(this.snapshot.revision + 1, this.simulation)
    this.listeners.forEach((listener) => listener(this.snapshot))
  }

  private reject(commandId: string, command: LoadgenCommand, error: AdapterError): CommandReceipt {
    return Object.freeze({
      commandId,
      commandType: command.type,
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: this.snapshot.revision,
      error: Object.freeze({
        ...error,
        details: error.details === null ? null : Object.freeze({ ...error.details }),
      }),
    })
  }
}
