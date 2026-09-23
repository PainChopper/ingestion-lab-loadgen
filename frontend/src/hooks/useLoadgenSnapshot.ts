import { useCallback, useMemo, useSyncExternalStore } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import type {
  LoadgenSnapshot,
  LoadgenTelemetrySnapshot,
} from '../model/loadgen'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'

export interface LoadgenSnapshotPresentation {
  readonly snapshot: LoadgenSnapshot
  readonly liveSnapshot: LoadgenTelemetrySnapshot
  readonly frozenObserved: boolean
}

export class PauseFrozenObservedDeriver {
  private lastRunning: LoadgenTelemetrySnapshot | null = null
  private frozen: LoadgenTelemetrySnapshot | null = null
  private sourceSnapshot: LoadgenTelemetrySnapshot | null = null
  private presentation: LoadgenTelemetrySnapshot | null = null

  derive(snapshot: LoadgenTelemetrySnapshot): {
    readonly snapshot: LoadgenTelemetrySnapshot
    readonly frozenObserved: boolean
  } {
    if (snapshot === this.sourceSnapshot && this.presentation !== null) {
      return {
        snapshot: this.presentation,
        frozenObserved: this.frozen !== null,
      }
    }

    if (snapshot.runState === 'running') {
      this.frozen = null
      this.lastRunning = snapshot.connectionState === 'connected' ? snapshot : null
      this.presentation = snapshot
    } else if (snapshot.runState === 'idle') {
      this.lastRunning = null
      this.frozen = null
      this.presentation = snapshot
    } else {
      if (this.frozen === null && this.lastRunning !== null) {
        this.frozen = this.lastRunning
      }
      this.presentation = this.frozen === null
        ? snapshot
        : freezeObservedTelemetry(this.frozen, snapshot)
    }

    this.sourceSnapshot = snapshot
    return {
      snapshot: this.presentation,
      frozenObserved: this.frozen !== null,
    }
  }
}

function freezeObservedTelemetry(
  observed: LoadgenTelemetrySnapshot,
  live: LoadgenTelemetrySnapshot,
): LoadgenTelemetrySnapshot {
  return Object.freeze({
    ...observed,
    revision: live.revision,
    adapterKind: live.adapterKind,
    connectionState: live.connectionState,
    runState: live.runState,
    policy: live.policy,
    reader: Object.freeze({
      ...observed.reader,
      workers: live.reader.workers,
      readBatchSize: live.reader.readBatchSize,
      state: live.reader.state,
    }),
    throttler: Object.freeze({
      ...observed.throttler,
      requestedTps: live.throttler.requestedTps,
      installationMode: live.throttler.installationMode,
      state: live.throttler.state,
    }),
    readerChannel: Object.freeze({
      ...observed.readerChannel,
      capacity: live.readerChannel.capacity,
    }),
    senderChannel: Object.freeze({
      ...observed.senderChannel,
      capacity: live.senderChannel.capacity,
    }),
    sender: Object.freeze({
      ...observed.sender,
      workers: live.sender.workers,
      simulatedDelayMs: live.sender.simulatedDelayMs,
      simulatedErrorRatePercent: live.sender.simulatedErrorRatePercent,
      timeoutMs: live.sender.timeoutMs,
      state: live.sender.state,
    }),
    http: Object.freeze({
      ...observed.http,
      connectionState: live.http.connectionState,
    }),
    target: Object.freeze({
      ...observed.target,
      connectionState: live.target.connectionState,
    }),
  })
}

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const derivation = useMemo(
    () => ({
      adapter,
      channelDeriver: new ChannelFlowStateDeriver(),
      pauseDeriver: new PauseFrozenObservedDeriver(),
      sourceSnapshot: null as LoadgenTelemetrySnapshot | null,
      presentation: null as LoadgenSnapshotPresentation | null,
    }),
    [adapter],
  )
  const subscribe = useCallback(
    (onStoreChange: () => void) => adapter.subscribe(onStoreChange),
    [adapter],
  )
  const getSnapshot = useCallback(
    () => {
      const liveSnapshot = derivation.adapter.getSnapshot()
      if (liveSnapshot === derivation.sourceSnapshot && derivation.presentation !== null) {
        return derivation.presentation
      }

      const paused = derivation.pauseDeriver.derive(liveSnapshot)
      const snapshot = derivation.channelDeriver.derive(
        paused.snapshot,
        performance.now(),
      )
      const presentation = Object.freeze({
        snapshot,
        liveSnapshot,
        frozenObserved: paused.frozenObserved,
      })
      derivation.sourceSnapshot = liveSnapshot
      derivation.presentation = presentation
      return presentation
    },
    [derivation],
  )

  return useSyncExternalStore(
    subscribe,
    getSnapshot,
    getSnapshot,
  )
}
