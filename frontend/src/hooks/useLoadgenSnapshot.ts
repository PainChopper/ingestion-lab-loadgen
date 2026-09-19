import { useCallback, useMemo, useSyncExternalStore } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const derivation = useMemo(
    () => ({ adapter, deriver: new ChannelFlowStateDeriver() }),
    [adapter],
  )
  const subscribe = useCallback(
    (onStoreChange: () => void) => adapter.subscribe(onStoreChange),
    [adapter],
  )
  const getSnapshot = useCallback(
    () => derivation.deriver.derive(
      derivation.adapter.getSnapshot(),
      performance.now(),
    ),
    [derivation],
  )

  return useSyncExternalStore(
    subscribe,
    getSnapshot,
    getSnapshot,
  )
}
