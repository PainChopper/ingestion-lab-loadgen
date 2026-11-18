import { useCallback, useState, useSyncExternalStore } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const [stored, setStored] = useState(() => ({ adapter, deriver: new ChannelFlowStateDeriver() }))
  let current = stored
  if (stored.adapter !== adapter) {
    current = { adapter, deriver: new ChannelFlowStateDeriver() }
    setStored(current)
  }
  const { deriver } = current

  const subscribe = useCallback(
    (onStoreChange: () => void) => adapter.subscribe(onStoreChange),
    [adapter],
  )
  const getSnapshot = useCallback(
    () => deriver.derivePresentation(adapter.getSnapshot(), performance.now()),
    [adapter, deriver],
  )

  return useSyncExternalStore(
    subscribe,
    getSnapshot,
    getSnapshot,
  )
}
