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
}

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const derivation = useMemo(
    () => ({
      adapter,
      channelDeriver: new ChannelFlowStateDeriver(),
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

      const snapshot = derivation.channelDeriver.derive(
        liveSnapshot,
        performance.now(),
      )
      const presentation = Object.freeze({
        snapshot,
        liveSnapshot,
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
