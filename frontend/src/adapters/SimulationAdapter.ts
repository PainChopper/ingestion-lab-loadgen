import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  AdapterError,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  RunState,
  ThrottlerInstallationMode,
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
import type {
  CapacityTelemetry,
  ChannelTelemetry,
  SimulationTelemetry,
} from '../model/simulation'

const TARGET_ENDPOINT = 'http://target:8080/ingest'
const RETRY_POLICY = Object.freeze({
  maxAttempts: RETRY_MAX_ATTEMPTS,
  backoffBaseMs: RETRY_BACKOFF_BASE_MS,
  backoffMultiplier: RETRY_BACKOFF_MULTIPLIER,
  jitterPercent: RETRY_JITTER_PERCENT,
  retryableStatusCodes: Object.freeze([...RETRYABLE_STATUS_CODES]),
  retryTimeouts: true,
})

const CONTROL_RANGES = Object.freeze({
  readerWorkers: Object.freeze({ min: 1, max: 7, step: 1 }),
  senderWorkers: Object.freeze({ min: 1, max: 32, step: 1 }),
  requestedTps: Object.freeze({ min: 0, max: 250_000, step: 5_000 }),
  readerChannelCapacity: Object.freeze({ min: 0, max: 12, step: 1 }),
  senderChannelCapacity: Object.freeze({ min: 0, max: 160, step: 10 }),
  readBatchSize: Object.freeze({ min: 1_000, max: 100_000, step: 1_000 }),
  httpTimeoutMs: Object.freeze({ min: 10, max: 5_000, step: 10 }),
  targetDelayMs: Object.freeze({ min: 0, max: 2_000, step: 10 }),
  targetErrorRatePercent: Object.freeze({ min: 0, max: 100, step: 1 }),
})

interface NumericRange {
  readonly min: number
  readonly max: number
  readonly step: number
}

interface AdapterState {
  revision: number
  runState: RunState
}

function numericControl(
  applied: number,
  range: NumericRange,
  unit: string,
  preview: number | null = null,
  pending: number | null = null,
): NumericControlSnapshot {
  return Object.freeze({
    applied,
    preview,
    pending,
    min: range.min,
    max: range.max,
    step: range.step,
    unit,
    applyMode: 'immediate',
  })
}

function capacityControl(
  capacity: CapacityTelemetry,
  range: NumericRange,
): NumericControlSnapshot {
  return numericControl(
    capacity.applied,
    range,
    'batches',
    capacity.preview,
    capacity.pending,
  )
}

function normalizeNumericValue(value: number, range: NumericRange): number {
  const bounded = Math.min(range.max, Math.max(range.min, value))
  const stepped =
    range.min + Math.round((bounded - range.min) / range.step) * range.step
  return Math.min(range.max, Math.max(range.min, stepped))
}

function freezeChannelSnapshot(
  channel: ChannelTelemetry,
  identity: {
    readonly id: 'reader-to-throttler' | 'throttler-to-sender'
    readonly from: 'reader' | 'throttler'
    readonly to: 'throttler' | 'sender'
    readonly range: NumericRange
  },
) {
  return Object.freeze({
    id: identity.id,
    from: identity.from,
    to: identity.to,
    capacity: capacityControl(channel.capacity, identity.range),
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
  state: AdapterState,
  simulation: FixedStepSimulation,
): LoadgenTelemetrySnapshot {
  const running = state.runState === 'running'
  const readerPoolLive = state.runState !== 'idle'
  const telemetry: SimulationTelemetry = simulation.telemetry(running)
  const config = simulation.config
  const readerCapacityTps = Math.round(telemetry.readerCapacityTps)
  const readerReadTps = Math.round(telemetry.readerTransactionsPerSecond)
  const senderWorkerStates = telemetry.sender.workerStates
  const drainingSenderWorkerStates = telemetry.sender.drainingWorkerStates
  const drainingWorkers =
    drainingSenderWorkerStates.idle +
    drainingSenderWorkerStates.inFlight +
    drainingSenderWorkerStates.backoff
  const readerLimitationReason = running &&
      telemetry.readerChannel.blockedSenders > 0 &&
      readerReadTps < readerCapacityTps
    ? 'downstream-backpressure'
    : null

  return Object.freeze({
    revision: state.revision,
    adapterKind: 'simulation',
    connectionState: 'connected',
    runState: state.runState,
    elapsedMs: telemetry.elapsedMs,
    totalTransactions: telemetry.totalTransactions,
    policy: null,
    reader: Object.freeze({
      id: 'reader',
      workers: numericControl(
        config.readerWorkers,
        CONTROL_RANGES.readerWorkers,
        'workers',
      ),
      liveWorkers: readerPoolLive ? config.readerWorkers : 0,
      idleWorkers: readerPoolLive && readerReadTps === 0 ? config.readerWorkers : 0,
      readingWorkers: readerPoolLive && readerReadTps > 0 ? config.readerWorkers : 0,
      blockedWorkers: 0,
      drainingWorkers: 0,
      drainingIdleWorkers: 0,
      drainingReadingWorkers: 0,
      drainingBlockedWorkers: 0,
      readBatchSize: numericControl(
        config.readBatchSize,
        CONTROL_RANGES.readBatchSize,
        'tx',
      ),
      readTps: readerReadTps,
      configuredCapacityTps: readerCapacityTps,
      limitationReason: readerLimitationReason,
      rowsRead: telemetry.readerChannel.sentTransactionsTotal,
      sourceError: null,
      state: state.runState,
    }),
    throttler: Object.freeze({
      id: 'throttler',
      requestedTps: numericControl(
        config.requestedTps,
        CONTROL_RANGES.requestedTps,
        'tx/s',
      ),
      installationMode: Object.freeze({
        applied: config.throttlerInstallationMode,
        pending: null,
        applyMode: 'immediate',
        writable: true,
        unavailableReason: null,
      }),
      admittedTps: Math.round(telemetry.admittedTransactionsPerSecond),
      limitedMs: Math.floor(telemetry.limitedMs),
      state: state.runState,
    }),
    readerChannel: freezeChannelSnapshot(telemetry.readerChannel, {
      id: 'reader-to-throttler',
      from: 'reader',
      to: 'throttler',
      range: CONTROL_RANGES.readerChannelCapacity,
    }),
    senderChannel: freezeChannelSnapshot(telemetry.senderChannel, {
      id: 'throttler-to-sender',
      from: 'throttler',
      to: 'sender',
      range: CONTROL_RANGES.senderChannelCapacity,
    }),
    sender: Object.freeze({
      id: 'sender',
      workers: numericControl(
        config.senderWorkers,
        CONTROL_RANGES.senderWorkers,
        'workers',
      ),
      liveWorkers: running ? telemetry.sender.workers.applied : 0,
      idleWorkers: running
        ? senderWorkerStates.idle + drainingSenderWorkerStates.idle
        : 0,
      inFlightWorkers: running
        ? senderWorkerStates.inFlight + drainingSenderWorkerStates.inFlight
        : 0,
      backoffWorkers: running
        ? senderWorkerStates.backoff + drainingSenderWorkerStates.backoff
        : 0,
      drainingWorkers: running ? drainingWorkers : 0,
      drainingIdleWorkers: running
        ? drainingSenderWorkerStates.idle
        : 0,
      drainingInFlightWorkers: running
        ? drainingSenderWorkerStates.inFlight
        : 0,
      drainingBackoffWorkers: running
        ? drainingSenderWorkerStates.backoff
        : 0,
      timeoutMs: numericControl(
        config.httpTimeoutMs,
        CONTROL_RANGES.httpTimeoutMs,
        'ms',
      ),
      retryPolicy: RETRY_POLICY,
      attemptedTps: Math.round(telemetry.attemptedTransactionsPerSecond),
      retryAttemptedTps: Math.round(
        telemetry.sender.retryAttemptedTransactionsPerSecond,
      ),
      terminalFailedTps: Math.round(
        telemetry.sender.terminalFailedTransactionsPerSecond,
      ),
      inFlightRequests: telemetry.http.inFlightRequests,
      attemptsStartedTotal: telemetry.http.requestsStartedTotal,
      retryAttemptsStartedTotal:
        telemetry.sender.retryAttemptsStartedTotal,
      successfulResponses: telemetry.http.requestsSucceededTotal,
      failedResponses: telemetry.http.responsesRejectedTotal,
      retries: telemetry.sender.retryAttemptsStartedTotal,
      timeoutsTotal: telemetry.http.requestsTimedOutTotal,
      terminalFailedBatchesTotal:
        telemetry.sender.terminalFailedBatchesTotal,
      terminalFailedTransactionsTotal:
        telemetry.sender.terminalFailedTransactionsTotal,
      ambiguousTimeoutTransactionsTotal:
        telemetry.sender.ambiguousTimeoutTransactionsTotal,
      duplicateRiskTransactionsTotal:
        telemetry.sender.duplicateRiskTransactionsTotal,
      ambiguousTerminalTransactionsTotal:
        telemetry.sender.ambiguousTerminalTransactionsTotal,
      state: state.runState,
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
      latencyP95Ms: telemetry.http.latestLatencyMs ?? (running ? null : 0),
    }),
    target: Object.freeze({
      id: 'target',
      endpoint: TARGET_ENDPOINT,
      acceptedTps: Math.round(telemetry.acceptedTransactionsPerSecond),
      rejectedTps: Math.round(telemetry.rejectedTransactionsPerSecond),
      latencyP95Ms:
        telemetry.http.latestResponseLatencyMs ?? (running ? null : 0),
      http200Responses: telemetry.http.requestsSucceededTotal,
      http503Responses: simulation.http503ResponsesTotal,
      connectionState: 'connected',
    }),
  })
}

export class SimulationAdapter implements LoadgenAdapter {
  readonly kind = 'simulation' as const

  private readonly listeners = new Set<LoadgenSnapshotListener>()
  private readonly state: AdapterState = { revision: 0, runState: 'idle' }
  private readonly simulation = new FixedStepSimulation(
    {
      readerWorkers: 4,
      senderWorkers: 3,
      requestedTps: 120_000,
      throttlerInstallationMode: 'installed',
      readBatchSize: 25_000,
      httpTimeoutMs: 500,
      targetDelayMs: 40,
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
    this.snapshot = freezeSnapshot(this.state, this.simulation)
    this.lastTickMs = Date.now()
    this.timer = setInterval(this.tick, SNAPSHOT_INTERVAL_MS)
  }

  getSnapshot = (): LoadgenTelemetrySnapshot => this.snapshot

  subscribe = (listener: LoadgenSnapshotListener): (() => void) => {
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
      case 'run':
        if (this.state.runState === 'running') {
          changed = this.advanceToNow()
        } else {
          this.state.runState = 'running'
          this.simulation.clearInstantaneousTelemetry()
          this.lastTickMs = Date.now()
          changed = true
        }
        break
      case 'pause':
        changed = this.advanceToNow()
        if (this.state.runState !== 'paused') {
          this.state.runState = 'paused'
          changed = true
        }
        break
      case 'reset':
        if (this.state.runState === 'running') {
          return this.reject(commandId, command, {
            code: 'invalid-state',
            message: 'reset is only available while idle or paused',
            retryable: false,
            details: null,
          })
        }
        changed = this.resetRunState()
        break
      case 'set-requested-tps':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'requested tps')
        }
        changed = this.updateConfig(
          'requestedTps',
          normalizeNumericValue(command.value, CONTROL_RANGES.requestedTps),
        )
        break
      case 'set-throttler-installation-mode':
        if (command.value !== 'installed' && command.value !== 'bypass') {
          return this.reject(commandId, command, {
            code: 'invalid-command',
            message: 'throttler installation mode must be installed or bypass',
            retryable: false,
            details: null,
          })
        }
        changed = this.updateInstallationMode(command.value)
        break
      case 'set-worker-count':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'worker count')
        }
        changed = this.updateConfig(
          'readerWorkers',
          normalizeNumericValue(command.value, CONTROL_RANGES.readerWorkers),
        )
        break
      case 'set-sender-workers':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'sender workers')
        }
        changed = this.updateConfig('senderWorkers', normalizeNumericValue(command.value, CONTROL_RANGES.senderWorkers))
        break
      case 'set-reader-channel-capacity':
      case 'set-sender-channel-capacity': {
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'channel capacity')
        }
        changed = this.advanceToNow()
        const channel = command.type === 'set-reader-channel-capacity' ? 1 : 2
        const range = channel === 1
          ? CONTROL_RANGES.readerChannelCapacity
          : CONTROL_RANGES.senderChannelCapacity
        changed =
          this.simulation.requestChannelCapacity(
            channel,
            normalizeNumericValue(command.value, range),
          ) || changed
        break
      }
      case 'set-read-batch-size':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber(commandId, command, 'read batch size')
        }
        changed = this.updateConfig(
          'readBatchSize',
          normalizeNumericValue(command.value, CONTROL_RANGES.readBatchSize),
        )
        break
      case 'set-http-timeout':
        if (!Number.isFinite(command.valueMs)) {
          return this.rejectInvalidNumber(commandId, command, 'http timeout')
        }
        changed = this.updateConfig(
          'httpTimeoutMs',
          normalizeNumericValue(command.valueMs, CONTROL_RANGES.httpTimeoutMs),
        )
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
    if (this.disposed || this.state.runState !== 'running') {
      this.lastTickMs = Date.now()
      return
    }
    if (this.advanceToNow()) this.publish()
  }

  private advanceToNow(): boolean {
    const now = Date.now()
    const deltaMs = Math.max(0, now - this.lastTickMs)
    this.lastTickMs = now
    if (this.state.runState !== 'running' || deltaMs === 0) return false

    this.pendingStepMs += deltaMs
    const steps = Math.floor(this.pendingStepMs / FIXED_STEP_MS)
    for (let index = 0; index < steps; index += 1) {
      this.simulation.advanceStep()
    }
    this.pendingStepMs -= steps * FIXED_STEP_MS
    return steps > 0
  }

  private updateConfig(
    key: keyof FixedStepSimulation['config'],
    value: number,
  ): boolean {
    const advanced = this.advanceToNow()
    if (this.simulation.config[key] === value) return advanced
    this.simulation.updateConfig({ [key]: value })
    return true
  }

  private updateInstallationMode(value: ThrottlerInstallationMode): boolean {
    const advanced = this.advanceToNow()
    if (this.simulation.config.throttlerInstallationMode === value) {
      return advanced
    }
    this.simulation.updateConfig({ throttlerInstallationMode: value })
    return true
  }

  private resetRunState(): boolean {
    const telemetry = this.simulation.telemetry(false)
    const changed =
      this.state.runState !== 'idle' ||
      telemetry.elapsedMs !== 0 ||
      telemetry.readerChannel.sentBatchesTotal !== 0 ||
      telemetry.senderChannel.sentBatchesTotal !== 0 ||
      telemetry.http.requestsStartedTotal !== 0 ||
      telemetry.readerChannel.capacity.pending !== null ||
      telemetry.senderChannel.capacity.pending !== null

    this.state.runState = 'idle'
    this.simulation.reset()
    this.pendingStepMs = 0
    this.lastTickMs = Date.now()
    return changed
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
    this.state.revision += 1
    this.snapshot = freezeSnapshot(this.state, this.simulation)
    this.listeners.forEach((listener) => listener(this.snapshot))
  }

  private reject(
    commandId: string,
    command: LoadgenCommand,
    error: AdapterError,
  ): CommandReceipt {
    return Object.freeze({
      commandId,
      commandType: command.type,
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: this.snapshot.revision,
      error: Object.freeze({
        ...error,
        details:
          error.details === null
            ? null
            : Object.freeze({ ...error.details }),
      }),
    })
  }
}
