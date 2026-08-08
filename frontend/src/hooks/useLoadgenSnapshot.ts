import { useCallback, useSyncExternalStore } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'

export function useLoadgenSnapshot(adapter: LoadgenAdapter) {
  const subscribe = useCallback(
    (onStoreChange: () => void) => adapter.subscribe(onStoreChange),
    [adapter],
  )

  return useSyncExternalStore(
    subscribe,
    adapter.getSnapshot,
    adapter.getSnapshot,
  )
}
