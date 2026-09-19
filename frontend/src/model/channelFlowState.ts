import type {
  ConnectionState,
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
  ChannelFlowState,
  ChannelSnapshot,
  ChannelTelemetrySnapshot,
  RunState,
} from './loadgen'

export const CHANNEL_PRESSURE_GREEN = '#79d957'
export const CHANNEL_PRESSURE_YELLOW = '#ffd31f'
export const CHANNEL_PRESSURE_RED = '#ff6748'

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

function hasRendezvousWaiter(channel: ChannelTelemetrySnapshot): boolean {
  return channel.capacity.applied === 0 && channel.blockedSenders > 0
}

export function effectiveChannelPressure(
  channel: ChannelTelemetrySnapshot,
): number {
  if (hasRendezvousWaiter(channel)) return 1

  return Math.max(
    occupancyPressure(channel.depthBatches, channel.capacity.applied),
    blockingPressure(channel.blockedSenders, channel.oldestBlockedSenderMs),
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

function channelMode(
  channel: ChannelTelemetrySnapshot,
  runState: RunState,
  connectionState: ConnectionState,
): Extract<ChannelFlowState, 'stopped' | 'connection-error'> | null {
  const missingTelemetry =
    channel.depthBatches === null ||
    channel.capacity.applied === null ||
    !Number.isFinite(channel.blockedSenders) ||
    !Number.isFinite(channel.oldestBlockedSenderMs)

  if (connectionState !== 'connected' || missingTelemetry) {
    return 'connection-error'
  }
  if (runState !== 'running') return 'stopped'
  return null
}

function pressureFlowState(displayedPressure: number): ChannelFlowState {
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

export function channelPressureColor(pressure: number): string {
  const bounded = clampPressure(pressure)
  if (bounded <= PRESSURE_MIDPOINT) {
    return mixHexColors(
      CHANNEL_PRESSURE_GREEN,
      CHANNEL_PRESSURE_YELLOW,
      bounded / PRESSURE_MIDPOINT,
    )
  }

  return mixHexColors(
    CHANNEL_PRESSURE_YELLOW,
    CHANNEL_PRESSURE_RED,
    (bounded - PRESSURE_MIDPOINT) / PRESSURE_MIDPOINT,
  )
}

interface ChannelDerivationState {
  displayedPressure: number
  observedAtMs: number | null
  pressureActive: boolean
}

export class ChannelFlowStateDeriver {
  private readonly channelStates = new Map<string, ChannelDerivationState>()
  private sourceSnapshot: LoadgenTelemetrySnapshot | null = null
  private derivedSnapshot: LoadgenSnapshot | null = null

  derive(
    snapshot: LoadgenTelemetrySnapshot,
    observedAtMs: number,
  ): LoadgenSnapshot {
    if (snapshot === this.sourceSnapshot && this.derivedSnapshot !== null) {
      return this.derivedSnapshot
    }

    const readerChannel = this.deriveChannel(
      snapshot.readerChannel,
      snapshot.runState,
      snapshot.connectionState,
      observedAtMs,
    )
    const senderChannel = this.deriveChannel(
      snapshot.senderChannel,
      snapshot.runState,
      snapshot.connectionState,
      observedAtMs,
    )
    const derived = Object.freeze({ ...snapshot, readerChannel, senderChannel })

    this.sourceSnapshot = snapshot
    this.derivedSnapshot = derived
    return derived
  }

  private deriveChannel(
    channel: ChannelTelemetrySnapshot,
    runState: RunState,
    connectionState: ConnectionState,
    observedAtMs: number,
  ): ChannelSnapshot {
    const state = this.channelStates.get(channel.id) ?? {
      displayedPressure: 0,
      observedAtMs: null,
      pressureActive: false,
    }
    const mode = channelMode(channel, runState, connectionState)

    if (mode === null) {
      const elapsedMs =
        state.pressureActive && state.observedAtMs !== null
          ? observedAtMs - state.observedAtMs
          : 0
      state.displayedPressure = hasRendezvousWaiter(channel)
        ? 1
        : moveDisplayedPressure(
            state.displayedPressure,
            effectiveChannelPressure(channel),
            elapsedMs,
          )
      state.pressureActive = Number.isFinite(observedAtMs) && elapsedMs >= 0
    } else {
      state.pressureActive = false
    }
    state.observedAtMs = Number.isFinite(observedAtMs) ? observedAtMs : null
    this.channelStates.set(channel.id, state)

    return Object.freeze({
      ...channel,
      displayedPressure: state.displayedPressure,
      flowState: mode ?? pressureFlowState(state.displayedPressure),
    })
  }
}
