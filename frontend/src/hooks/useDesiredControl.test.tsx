import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useDesiredControl } from './useDesiredControl'

describe('useDesiredControl', () => {
  it('keeps accepted desired pending through stale snapshots until an exact newer snapshot', async () => {
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const view = renderHook(
      ({ applied, revision }) => useDesiredControl({
        applied,
        revision,
        available: true,
        dispatch,
      }),
      { initialProps: { applied: 4, revision: 10 } },
    )

    act(() => view.result.current.preview(7))
    expect(view.result.current).toMatchObject({ desired: 7, phase: 'preview' })
    await act(async () => { await view.result.current.commit() })
    expect(dispatch).toHaveBeenCalledOnce()
    expect(dispatch).toHaveBeenCalledWith(7)
    expect(view.result.current).toMatchObject({ desired: 7, phase: 'pending' })

    view.rerender({ applied: 4, revision: 11 })
    expect(view.result.current).toMatchObject({ desired: 7, phase: 'pending' })
    view.rerender({ applied: 7, revision: 11 })
    expect(view.result.current).toMatchObject({ desired: 7, phase: 'idle' })
  })

  it('rolls rejected and unavailable commands back to the latest applied value', async () => {
    let resolveReceipt!: (receipt: { accepted: boolean }) => void
    let rejectReceipt!: (reason: Error) => void
    const dispatch = vi.fn(() => new Promise<{ accepted: boolean }>((resolve, reject) => {
      resolveReceipt = resolve
      rejectReceipt = reject
    }))
    const view = renderHook(
      ({ applied, revision }) => useDesiredControl({
        applied,
        revision,
        available: true,
        dispatch,
      }),
      { initialProps: { applied: 4, revision: 10 } },
    )

    act(() => view.result.current.preview(8))
    let rejected!: Promise<boolean>
    act(() => { rejected = view.result.current.commit() })
    expect(dispatch).toHaveBeenNthCalledWith(1, 8)
    view.rerender({ applied: 5, revision: 11 })
    expect(view.result.current).toMatchObject({ applied: 5, desired: 8, phase: 'pending' })
    await act(async () => {
      resolveReceipt({ accepted: false })
      expect(await rejected).toBe(false)
    })
    expect(view.result.current).toMatchObject({
      desired: 5,
      phase: 'idle',
      error: 'Change rejected',
    })

    act(() => view.result.current.preview(9))
    let unavailable!: Promise<boolean>
    act(() => { unavailable = view.result.current.commit() })
    expect(dispatch).toHaveBeenNthCalledWith(2, 9)
    view.rerender({ applied: 6, revision: 12 })
    expect(view.result.current).toMatchObject({ applied: 6, desired: 9, phase: 'pending' })
    await act(async () => {
      rejectReceipt(new Error('offline'))
      expect(await unavailable).toBe(false)
    })
    expect(dispatch).toHaveBeenCalledTimes(2)
    expect(view.result.current).toMatchObject({
      desired: 6,
      phase: 'idle',
      error: 'Change unavailable',
    })
  })

  it('cancels preview without dispatch and blocks a second commit while pending', async () => {
    let resolveReceipt: ((receipt: { accepted: boolean }) => void) | undefined
    const dispatch = vi.fn(() => new Promise<{ accepted: boolean }>((resolve) => {
      resolveReceipt = resolve
    }))
    const view = renderHook(() => useDesiredControl({
      applied: 4,
      revision: 10,
      available: true,
      dispatch,
    }))

    act(() => view.result.current.preview(6))
    act(() => view.result.current.cancel())
    expect(view.result.current).toMatchObject({ desired: 4, phase: 'idle' })
    expect(dispatch).not.toHaveBeenCalled()

    act(() => view.result.current.preview(7))
    let first!: Promise<boolean>
    act(() => { first = view.result.current.commit() })
    await act(async () => {
      expect(await view.result.current.commit(8)).toBe(false)
    })
    expect(dispatch).toHaveBeenCalledOnce()
    await act(async () => {
      resolveReceipt?.({ accepted: true })
      await first
    })
    expect(view.result.current.phase).toBe('pending')
  })
})
