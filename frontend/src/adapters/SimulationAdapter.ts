import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  AdapterError,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
} from '../model/loadgen'
import { FIXED_STEP_MS, FixedStepSimulation, SNAPSHOT_INTERVAL_MS } from '../model/simulation'
import type { ChannelTelemetry, SimulationConfig, SimulationTelemetry } from '../model/simulation'

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
    depthBatches: channel.depthBatches,
    blockedSenders: channel.blockedSenders,
    oldestBlockedSenderMs: channel.oldestBlockedSenderMs,
    inputTransactionsPerSecond: Math.round(channel.inputTransactionsPerSecond),
    outputTransactionsPerSecond: Math.round(channel.outputTransactionsPerSecond),
  })
}

function freezeSnapshot(
  revision: number,
  simulation: FixedStepSimulation,
): LoadgenTelemetrySnapshot {
  const telemetry: SimulationTelemetry = simulation.telemetry()
  const config = simulation.config
  const readerReadTps = Math.round(telemetry.readerTransactionsPerSecond)
  const senderWorkerStates = telemetry.sender.workerStates
  const drainingSenderWorkerStates = telemetry.sender.drainingWorkerStates
  const drainingWorkers =
    drainingSenderWorkerStates.idle +
    drainingSenderWorkerStates.inFlight +
    drainingSenderWorkerStates.backoff

  return Object.freeze({
    revision,
    adapterKind: 'simulation',
    connectionState: 'connected',
    runState: 'running',
    elapsedMs: telemetry.elapsedMs,
    totalTransactions: telemetry.totalTransactions,
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
      rowsRead: telemetry.rowsRead,
      sourceError: null,
    }),
    throttler: Object.freeze({
      id: 'throttler',
      requestedTps: numericControl(config.requestedTps, RANGES.requestedTps, 'tx/s'),
      installed: Object.freeze({
        applied: config.throttlerInstalled,
        applyMode: 'immediate',
      }),
      admittedTps: Math.round(telemetry.admittedTransactionsPerSecond),
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
    if (this.disposed) {
      return this.reject({
        code: 'disposed',
        message: 'simulation adapter is disposed',
      })
    }

    let changed = false

    switch (command.type) {
      case 'set-requested-tps':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber('requested tps')
        }
        changed = this.updateConfig(
          'requestedTps',
          normalizeNumericValue(command.value, RANGES.requestedTps),
        )
        break
      case 'set-reader-workers':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber('worker count')
        }
        changed = this.updateConfig(
          'readerWorkers',
          normalizeNumericValue(command.value, RANGES.readerWorkers),
        )
        break
      case 'set-sender-workers':
        if (!Number.isFinite(command.value)) {
          return this.rejectInvalidNumber('sender workers')
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
        return this.reject({
          code: 'unavailable',
          message: 'command is not available in the simulation adapter',
        })
    }

    if (changed) this.publish()

    return Object.freeze({
      accepted: true,
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

  private rejectInvalidNumber(field: string): CommandReceipt {
    return this.reject({ code: 'invalid-command', message: `${field} must be a finite number` })
  }

  private publish(): void {
    this.snapshot = freezeSnapshot(this.snapshot.revision + 1, this.simulation)
    this.listeners.forEach((listener) => listener(this.snapshot))
  }

  private reject(error: AdapterError): CommandReceipt {
    return Object.freeze({ accepted: false, error: Object.freeze(error) })
  }
}
