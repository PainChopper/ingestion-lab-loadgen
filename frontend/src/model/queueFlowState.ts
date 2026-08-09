import type {
  ConnectionState,
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  QueueFlowState,
  QueueSnapshot,
  QueueTelemetrySnapshot,
  RunState,
} from './loadgen'

export const QUEUE_PRESSURE_GREEN = '#79d957'
export const QUEUE_PRESSURE_YELLOW = '#ffd31f'
export const QUEUE_PRESSURE_RED = '#ff6748'

const PRESSURE_UNITS_PER_SECOND = 2
const PRESSURE_MIDPOINT = 0.5
const BLOCKING_NOISE_MAX_MS = 100
const BLOCKING_SATURATION_MS = 500

function clampPressure(value: number): number {
  return Math.min(1, Math.max(0, value))
}

export function occupancyPressure(
  depthBatches: number | null,
  appliedCapacity: number | null,
): number {
  if (
    depthBatches === null ||
    appliedCapacity === null ||
    !Number.isFinite(depthBatches) ||
    !Number.isFinite(appliedCapacity) ||
    appliedCapacity <= 0
  ) {
    return 0
  }

  return clampPressure(depthBatches / appliedCapacity)
}

export function blockingPressure(
  blockedSenders: number,
  oldestBlockedSenderMs: number,
): number {
  if (
    blockedSenders <= 0 ||
    !Number.isFinite(oldestBlockedSenderMs) ||
    oldestBlockedSenderMs <= BLOCKING_NOISE_MAX_MS
  ) {
    return 0
  }

  return clampPressure(
    (oldestBlockedSenderMs - BLOCKING_NOISE_MAX_MS) /
      (BLOCKING_SATURATION_MS - BLOCKING_NOISE_MAX_MS),
  )
}

export function effectiveQueuePressure(
  queue: QueueTelemetrySnapshot,
): number {
  return Math.max(
    occupancyPressure(queue.depthBatches, queue.capacity.applied),
    blockingPressure(queue.blockedSenders, queue.oldestBlockedSenderMs),
  )
}

function moveDisplayedPressure(
  current: number,
  target: number,
  elapsedMs: number,
): number {
  if (!Number.isFinite(elapsedMs) || elapsedMs <= 0) return current

  const difference = target - current
  const maxChange = PRESSURE_UNITS_PER_SECOND * elapsedMs / 1_000
  if (Math.abs(difference) <= maxChange) return target

  const next = current + Math.sign(difference) * maxChange
  return Math.round(next * 1_000_000) / 1_000_000
}

function queueMode(
  queue: QueueTelemetrySnapshot,
  runState: RunState,
  connectionState: ConnectionState,
): Extract<QueueFlowState, 'stopped' | 'connection-error'> | null {
  const missingTelemetry =
    queue.depthBatches === null ||
    queue.capacity.applied === null ||
    !Number.isFinite(queue.blockedSenders) ||
    !Number.isFinite(queue.oldestBlockedSenderMs)

  if (connectionState !== 'connected' || missingTelemetry) {
    return 'connection-error'
  }
  if (runState !== 'running') return 'stopped'
  return null
}

function pressureFlowState(displayedPressure: number): QueueFlowState {
  if (displayedPressure === 1) return 'backpressure'
  if (displayedPressure >= PRESSURE_MIDPOINT) return 'near-limit'
  return 'normal'
}

function mixHexColors(start: string, end: string, amount: number): string {
  const channels = [1, 3, 5].map((offset) => {
    const startChannel = Number.parseInt(start.slice(offset, offset + 2), 16)
    const endChannel = Number.parseInt(end.slice(offset, offset + 2), 16)
    return Math.round(startChannel + (endChannel - startChannel) * amount)
      .toString(16)
      .padStart(2, '0')
  })
  return `#${channels.join('')}`
}

export function queuePressureColor(pressure: number): string {
  const bounded = clampPressure(pressure)
  if (bounded <= PRESSURE_MIDPOINT) {
    return mixHexColors(
      QUEUE_PRESSURE_GREEN,
      QUEUE_PRESSURE_YELLOW,
      bounded / PRESSURE_MIDPOINT,
    )
  }

  return mixHexColors(
    QUEUE_PRESSURE_YELLOW,
    QUEUE_PRESSURE_RED,
    (bounded - PRESSURE_MIDPOINT) / PRESSURE_MIDPOINT,
  )
}

interface QueueDerivationState {
  displayedPressure: number
  observedAtMs: number | null
  pressureActive: boolean
}

export class QueueFlowStateDeriver {
  private readonly queueStates = new Map<string, QueueDerivationState>()
  private sourceSnapshot: LoadgenTelemetrySnapshot | null = null
  private derivedSnapshot: LoadgenSnapshot | null = null

  derive(
    snapshot: LoadgenTelemetrySnapshot,
    observedAtMs: number,
  ): LoadgenSnapshot {
    if (snapshot === this.sourceSnapshot && this.derivedSnapshot !== null) {
      return this.derivedSnapshot
    }

    const queue1 = this.deriveQueue(
      snapshot.queue1,
      snapshot.runState,
      snapshot.connectionState,
      observedAtMs,
    )
    const queue2 = this.deriveQueue(
      snapshot.queue2,
      snapshot.runState,
      snapshot.connectionState,
      observedAtMs,
    )
    const derived = Object.freeze({ ...snapshot, queue1, queue2 })

    this.sourceSnapshot = snapshot
    this.derivedSnapshot = derived
    return derived
  }

  private deriveQueue(
    queue: QueueTelemetrySnapshot,
    runState: RunState,
    connectionState: ConnectionState,
    observedAtMs: number,
  ): QueueSnapshot {
    const state = this.queueStates.get(queue.id) ?? {
      displayedPressure: 0,
      observedAtMs: null,
      pressureActive: false,
    }
    const mode = queueMode(queue, runState, connectionState)

    if (mode === null) {
      const elapsedMs =
        state.pressureActive && state.observedAtMs !== null
          ? observedAtMs - state.observedAtMs
          : 0
      state.displayedPressure = moveDisplayedPressure(
        state.displayedPressure,
        effectiveQueuePressure(queue),
        elapsedMs,
      )
      state.pressureActive = Number.isFinite(observedAtMs) && elapsedMs >= 0
    } else {
      state.pressureActive = false
    }
    state.observedAtMs = Number.isFinite(observedAtMs) ? observedAtMs : null
    this.queueStates.set(queue.id, state)

    return Object.freeze({
      ...queue,
      displayedPressure: state.displayedPressure,
      flowState: mode ?? pressureFlowState(state.displayedPressure),
    })
  }
}
