import type { LoadgenAdapter, LoadgenSnapshotListener } from './LoadgenAdapter'
import type {
  AdapterError,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
} from '../model/loadgen'
import { FIXED_STEP_MS, FixedStepSimulation, SNAPSHOT_INTERVAL_MS } from '../model/simulation'
import { CounterRateCalculator, type CounterRates } from '../model/counterRates'
import type { ChannelTelemetry, SimulationConfig } from '../model/simulation'
import type { WireConfig, WireSnapshot } from '../model/wireSnapshot'

const RANGES = Object.freeze({
  readerWorkers: Object.freeze({ min: 1, max: 7 }),
  senderWorkers: Object.freeze({ min: 1, max: 32 }),
  requestedTps: Object.freeze({ min: 0, max: 250_000 }),
})

interface NumericRange {
  readonly min: number
  readonly max: number
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
    unit,
    applyMode,
  })
}

export function createSimulationWireSnapshot(simulation: FixedStepSimulation): WireSnapshot {
  const telemetry = simulation.telemetry()
  const config = simulation.config
  const { capacity: _readerCapacity, ...readerChannel } = telemetry.readerChannel
  const { capacity: _senderCapacity, ...senderChannel } = telemetry.senderChannel
  return {
    capturedAtMs: telemetry.capturedAtMs,
    run: { state: 'running', ...telemetry.run },
    reader: {
      workers: config.readerWorkers,
      workerStates: [...telemetry.reader.workerStates],
      rowsRead: telemetry.reader.rowsRead,
      sourceError: null,
    },
    throttler: { requestedTps: config.requestedTps, installed: config.throttlerInstalled },
    sender: { workers: config.senderWorkers, workerStates: [...telemetry.sender.workerStates] },
    readerChannel,
    senderChannel,
  }
}

export function createSimulationWireConfig(simulation: FixedStepSimulation): Readonly<WireConfig> {
  const telemetry = simulation.telemetry()
  return Object.freeze({
    readerWorkersMax: RANGES.readerWorkers.max,
    senderWorkersMax: RANGES.senderWorkers.max,
    requestedTpsMax: RANGES.requestedTps.max,
    readBatchSize: simulation.config.readBatchSize,
    readerChannelCapacity: telemetry.readerChannel.capacity,
    senderChannelCapacity: telemetry.senderChannel.capacity,
    sourcePath: 'simulation',
  })
}

function freezeChannelSnapshot(
  channel: WireSnapshot['readerChannel'],
  capacity: ChannelTelemetry['capacity'],
  rates: CounterRates['readerChannel'],
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
    capacity,
    depthBatches: channel.depthBatches,
    blockedSenders: channel.blockedSenders,
    oldestBlockedSenderMs: channel.oldestBlockedSenderMs,
    ...rates,
  })
}

function freezeSnapshot(
  revision: number,
  simulation: FixedStepSimulation,
  calculator: CounterRateCalculator,
  config: Readonly<WireConfig>,
): LoadgenTelemetrySnapshot {
  const wire = createSimulationWireSnapshot(simulation)
  const rates = calculator.observe(wire)

  return Object.freeze({
    revision,
    adapterKind: 'simulation',
    connectionState: 'connected',
    runState: wire.run.state,
    elapsedMs: wire.run.elapsedMs,
    totalTransactions: wire.run.totalTransactions,
    reader: Object.freeze({
      id: 'reader',
      workers: numericControl(wire.reader.workers, RANGES.readerWorkers, 'workers'),
      workerStates: Object.freeze(wire.reader.workerStates),
      readBatchSize: config.readBatchSize,
      sourcePath: config.sourcePath,
      readTps: rates.readTps,
      rowsRead: wire.reader.rowsRead,
      sourceError: wire.reader.sourceError,
    }),
    throttler: Object.freeze({
      id: 'throttler',
      requestedTps: numericControl(wire.throttler.requestedTps, RANGES.requestedTps, 'tx/s'),
      installed: Object.freeze({
        applied: wire.throttler.installed,
        applyMode: 'immediate',
      }),
      admittedTps: rates.admittedTps,
    }),
    readerChannel: freezeChannelSnapshot(
      wire.readerChannel,
      config.readerChannelCapacity,
      rates.readerChannel,
      {
        id: 'reader-to-throttler',
        from: 'reader',
        to: 'throttler',
      },
    ),
    senderChannel: freezeChannelSnapshot(
      wire.senderChannel,
      config.senderChannelCapacity,
      rates.senderChannel,
      {
        id: 'throttler-to-sender',
        from: 'throttler',
        to: 'sender',
      },
    ),
    sender: Object.freeze({
      id: 'sender',
      workers: numericControl(wire.sender.workers, RANGES.senderWorkers, 'workers'),
      workerStates: Object.freeze(wire.sender.workerStates),
    }),
  })
}

export class SimulationAdapter implements LoadgenAdapter {
  readonly kind = 'simulation' as const

  private readonly listeners = new Set<LoadgenSnapshotListener>()
  private readonly rateCalculator = new CounterRateCalculator()
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
  private readonly config = createSimulationWireConfig(this.simulation)
  private timer: ReturnType<typeof setInterval> | null
  private lastTickMs: number
  private pendingStepMs = 0
  private disposed = false

  constructor() {
    this.snapshot = freezeSnapshot(0, this.simulation, this.rateCalculator, this.config)
    this.lastTickMs = Date.now()
    this.timer = setInterval(this.tick, SNAPSHOT_INTERVAL_MS)
  }

  getSnapshot = (): LoadgenTelemetrySnapshot => this.snapshot
  getConfig = (): Readonly<WireConfig> => this.config

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
        if (
          !Number.isSafeInteger(command.value) ||
          command.value < 0 ||
          command.value > RANGES.requestedTps.max
        ) {
          return this.reject({
            code: 'invalid-command',
            message: 'requested tps must be an integer within 0..max',
          })
        }
        changed = this.updateConfig('requestedTps', command.value)
        break
      case 'set-reader-workers':
        if (
          !Number.isSafeInteger(command.value) ||
          command.value < RANGES.readerWorkers.min ||
          command.value > RANGES.readerWorkers.max
        ) {
          return this.reject({
            code: 'invalid-command',
            message: 'worker count must be an integer within min..max',
          })
        }
        changed = this.updateConfig('readerWorkers', command.value)
        break
      case 'set-sender-workers':
        if (
          !Number.isSafeInteger(command.value) ||
          command.value < RANGES.senderWorkers.min ||
          command.value > RANGES.senderWorkers.max
        ) {
          return this.reject({
            code: 'invalid-command',
            message: 'sender workers must be an integer within min..max',
          })
        }
        changed = this.updateConfig('senderWorkers', command.value)
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

  private publish(): void {
    this.snapshot = freezeSnapshot(
      this.snapshot.revision + 1,
      this.simulation,
      this.rateCalculator,
      this.config,
    )
    this.listeners.forEach((listener) => listener(this.snapshot))
  }

  private reject(error: AdapterError): CommandReceipt {
    return Object.freeze({ accepted: false, error: Object.freeze(error) })
  }
}
