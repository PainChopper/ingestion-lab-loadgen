import { act, renderHook } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import { useLoadgenSnapshot } from './useLoadgenSnapshot'

it('keeps one presentation per live snapshot and subscribes to the current adapter', async () => {
  vi.useFakeTimers()
  const first = new SimulationAdapter()
  const second = new SimulationAdapter()
  const originalSubscribe = first.subscribe
  const unsubscribe = vi.fn()
  vi.spyOn(first, 'subscribe').mockImplementation((listener) => {
    const detach = originalSubscribe(listener)
    return () => {
      unsubscribe()
      detach()
    }
  })

  try {
    const view = renderHook(useLoadgenSnapshot, { initialProps: first })
    const initial = view.result.current
    expect(initial.liveSnapshot).toBe(first.getSnapshot())
    view.rerender(first)
    expect(view.result.current).toBe(initial)

    view.rerender(second)
    expect(unsubscribe).toHaveBeenCalledOnce()
    const switched = view.result.current
    expect(switched).not.toBe(initial)
    expect(switched.liveSnapshot).toBe(second.getSnapshot())

    await act(async () => { await second.dispatch({ type: 'set-requested-tps', value: 75_000 }) })
    const updated = view.result.current
    expect(updated).not.toBe(switched)
    expect(updated.liveSnapshot).toBe(second.getSnapshot())
    expect(updated.snapshot.throttler.requestedTps.applied).toBe(75_000)
    view.rerender(second)
    expect(view.result.current).toBe(updated)
  } finally {
    first.dispose()
    second.dispose()
    vi.useRealTimers()
  }
})
