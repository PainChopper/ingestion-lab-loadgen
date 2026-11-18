import { useCallback, useMemo, useSyncExternalStore } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const subscribe = useCallback(
    (onStoreChange: () => void) => adapter.subscribe(onStoreChange),
    [adapter],
  )
  const getSnapshot = useMemo(() => {
    const channelDeriver = new ChannelFlowStateDeriver()
    return () => channelDeriver.derivePresentation(adapter.getSnapshot(), performance.now())
  }, [adapter])

  return useSyncExternalStore(
    subscribe,
    getSnapshot,
    getSnapshot,
  )
}
