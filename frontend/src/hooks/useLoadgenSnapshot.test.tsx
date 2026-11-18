import { act, renderHook } from '@testing-library/react'
import { StrictMode } from 'react'
import { expect, it, vi } from 'vitest'
import type { LoadgenAdapter, LoadgenSnapshotListener } from '../adapters/LoadgenAdapter'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenTelemetrySnapshot } from '../model/loadgen'
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

it('preserves the pressure history across rerenders and starts fresh on adapter change', () => {
  vi.useFakeTimers()
  const now = vi.spyOn(performance, 'now').mockReturnValue(0)
  const simulation = new SimulationAdapter()

  try {
    const base = simulation.getSnapshot()
    function createAdapter(depthBatches: number) {
      let snapshot: LoadgenTelemetrySnapshot = {
        ...base,
        connectionState: 'connected',
        runState: 'running',
        readerChannel: {
          ...base.readerChannel,
          capacity: { ...base.readerChannel.capacity, applied: 4 },
          depthBatches,
          blockedSenders: 0,
          oldestBlockedSenderMs: 0,
        },
      }
      const listeners = new Set<LoadgenSnapshotListener>()
      const adapter: LoadgenAdapter = {
        kind: base.adapterKind,
        getSnapshot: () => snapshot,
        subscribe: (listener) => {
          listeners.add(listener)
          return () => { listeners.delete(listener) }
        },
        dispatch: vi.fn(),
        dispose: () => listeners.clear(),
      }
      return {
        adapter,
        publish: (depth: number) => {
          snapshot = {
            ...snapshot,
            revision: snapshot.revision + 1,
            readerChannel: { ...snapshot.readerChannel, depthBatches: depth },
          }
          for (const listener of listeners) listener(snapshot)
        },
      }
    }

    const first = createAdapter(0)
    const second = createAdapter(2)
    const view = renderHook(useLoadgenSnapshot, {
      initialProps: first.adapter,
      wrapper: StrictMode,
    })
    expect(view.result.current.snapshot.readerChannel.displayedPressure).toBe(0)

    now.mockReturnValue(250)
    act(() => first.publish(4))
    const updated = view.result.current
    expect(updated.snapshot.readerChannel.displayedPressure).toBe(0.5)

    now.mockReturnValue(300)
    for (let index = 0; index < 3; index += 1) {
      view.rerender(first.adapter)
      expect(view.result.current).toBe(updated)
      expect(view.result.current.snapshot.readerChannel.displayedPressure).toBe(0.5)
    }

    view.rerender(second.adapter)
    expect(view.result.current.liveSnapshot).toBe(second.adapter.getSnapshot())
    expect(view.result.current.snapshot.readerChannel.displayedPressure).toBe(0)
  } finally {
    simulation.dispose()
    now.mockRestore()
    vi.useRealTimers()
  }
})
