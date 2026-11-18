import { act, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { StrictMode, useLayoutEffect, useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useDesiredControl } from './useDesiredControl'

function renderPreviewParent(removeChild: boolean) {
  const dispatch = vi.fn()
  const childCancelled = vi.fn()

  function Child({ cancel, trigger }: { cancel: () => void; trigger: boolean }) {
    useLayoutEffect(() => {
      if (removeChild) {
        return () => {
          childCancelled('cleanup')
          cancel()
        }
      }
    }, [cancel])

    useLayoutEffect(() => {
      if (trigger) {
        childCancelled('update')
        cancel()
      }
    }, [cancel, trigger])
    return <span>Child</span>
  }

  function Parent() {
    const { cancel, desired, phase, preview } = useDesiredControl({
      applied: 4,
      revision: 10,
      available: true,
      dispatch,
    })
    const [trigger, setTrigger] = useState(false)
    const handlePreview = () => {
      preview(7)
      setTrigger(true)
    }
    return (
      <>
        <button type="button" onClick={handlePreview}>
          Preview
        </button>
        <output aria-label="Parent control">
          {phase}: {desired}
        </output>
        {(!removeChild || !trigger) && <Child cancel={cancel} trigger={trigger} />}
      </>
    )
  }

  render(<Parent />)
  return { dispatch, childCancelled }
}

describe('useDesiredControl', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('cancels preview from child layout cleanup while the parent stays mounted', () => {
    const { dispatch, childCancelled } = renderPreviewParent(true)
    const parent = screen.getByLabelText('Parent control')
    expect(parent.textContent).toBe('idle: 4')
    expect(screen.getByText('Child')).toBeDefined()
    expect(childCancelled).not.toHaveBeenCalled()

    act(() => fireEvent.click(screen.getByRole('button', { name: 'Preview' })))

    expect(childCancelled.mock.calls).toEqual([['cleanup']])
    expect(screen.queryByText('Child')).toBeNull()
    expect(screen.getByLabelText('Parent control')).toBe(parent)
    expect(dispatch).not.toHaveBeenCalled()
    expect(parent.textContent).toBe('idle: 4')
  })

  it('cancels preview from the mounted child layout effect on update', () => {
    const { dispatch, childCancelled } = renderPreviewParent(false)
    const parent = screen.getByLabelText('Parent control')
    const child = screen.getByText('Child')
    expect(parent.textContent).toBe('idle: 4')
    expect(childCancelled).not.toHaveBeenCalled()

    act(() => fireEvent.click(screen.getByRole('button', { name: 'Preview' })))

    expect(childCancelled.mock.calls).toEqual([['update']])
    expect(screen.getByText('Child')).toBe(child)
    expect(screen.getByLabelText('Parent control')).toBe(parent)
    expect(dispatch).not.toHaveBeenCalled()
    expect(parent.textContent).toBe('idle: 4')
  })

  it('previews and commits after StrictMode restarts layout effects', async () => {
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const { result } = renderHook(
      () =>
        useDesiredControl({
          applied: 4,
          revision: 10,
          available: true,
          dispatch,
        }),
      { wrapper: StrictMode },
    )

    act(() => result.current.preview(7))
    expect(result.current).toMatchObject({ desired: 7, phase: 'preview' })

    await act(async () => {
      expect(await result.current.commit()).toBe(true)
    })
    expect(dispatch.mock.calls).toEqual([[7]])
    expect(result.current).toMatchObject({ desired: 7, phase: 'pending' })
  })

  it('ignores a late dispatch failure after unmount and leaves saved callbacks inactive', async () => {
    let rejectReceipt!: (reason: Error) => void
    const receipt = new Promise<{ accepted: boolean }>((_, reject) => {
      rejectReceipt = reject
    })
    const dispatch = vi.fn().mockResolvedValue({ accepted: true }).mockReturnValueOnce(receipt)
    const { result, unmount } = renderHook(() =>
      useDesiredControl({
        applied: 4,
        revision: 10,
        available: true,
        dispatch,
      }),
    )
    const { commit, preview } = result.current

    let inFlight!: Promise<boolean>
    act(() => {
      inFlight = commit(7)
    })
    expect(result.current).toMatchObject({ desired: 7, phase: 'pending' })
    unmount()

    await act(async () => {
      rejectReceipt(new Error('offline'))
      expect(await inFlight).toBe(false)
      preview(8)
      expect(await commit(8)).toBe(false)
    })
    expect(dispatch.mock.calls).toEqual([[7]])
  })

  it('keeps the desired value through stale snapshots and confirms it at five seconds', async () => {
    vi.useFakeTimers()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const view = renderHook(
      ({ applied, revision }) =>
        useDesiredControl({
          applied,
          revision,
          available: true,
          dispatch,
        }),
      { initialProps: { applied: true, revision: 10 } },
    )

    act(() => view.result.current.preview(false))
    expect(view.result.current).toMatchObject({ desired: false, phase: 'preview' })
    await act(async () => {
      await view.result.current.commit()
    })
    expect(dispatch).toHaveBeenCalledOnce()
    expect(dispatch).toHaveBeenCalledWith(false)
    expect(view.result.current).toMatchObject({ desired: false, phase: 'pending' })

    view.rerender({ applied: true, revision: 11 })
    expect(view.result.current).toMatchObject({ desired: false, phase: 'pending' })
    act(() => vi.advanceTimersByTime(4_999))
    expect(view.result.current).toMatchObject({ applied: true, desired: false, phase: 'pending' })
    expect(vi.getTimerCount()).toBe(1)
    act(() => vi.advanceTimersByTime(1))
    view.rerender({ applied: false, revision: 11 })
    expect(view.result.current).toMatchObject({ applied: false, desired: false, phase: 'idle' })
    expect(vi.getTimerCount()).toBe(0)
    act(() => vi.advanceTimersByTime(1_000))
    expect(view.result.current).toMatchObject({ applied: false, desired: false, phase: 'idle' })
  })

  it('returns to the latest applied value six seconds after a delayed acceptance', async () => {
    vi.useFakeTimers()
    let accept!: (receipt: { accepted: boolean }) => void
    const receipt = new Promise<{ accepted: boolean }>((resolve) => {
      accept = resolve
    })
    const dispatch = vi.fn().mockResolvedValue({ accepted: true }).mockReturnValueOnce(receipt)
    const props = { applied: 4, revision: 10, available: true, dispatch }
    const { result, rerender } = renderHook(useDesiredControl<number>, { initialProps: props })
    let submission!: Promise<boolean>
    act(() => {
      submission = result.current.commit(8)
    })
    act(() => vi.advanceTimersByTime(1_000))
    rerender({ ...props, applied: 5, revision: 11 })
    expect(result.current).toMatchObject({ applied: 5, desired: 8, phase: 'pending' })
    expect(vi.getTimerCount()).toBe(0)
    act(() => vi.advanceTimersByTime(1_000))
    await act(async () => {
      accept({ accepted: true })
      expect(await submission).toBe(true)
    })

    act(() => vi.advanceTimersByTime(1_000))
    rerender({ ...props, applied: 6, revision: 12 })
    act(() => vi.advanceTimersByTime(4_999))
    expect(result.current).toMatchObject({ applied: 6, desired: 8, phase: 'pending' })
    act(() => vi.advanceTimersByTime(1))
    expect(result.current).toMatchObject({
      applied: 6,
      desired: 6,
      phase: 'idle',
      error: null,
      available: true,
    })
    expect(vi.getTimerCount()).toBe(0)
    await act(async () => {
      expect(await result.current.commit(9)).toBe(true)
    })
    expect(dispatch.mock.calls).toEqual([[8], [9]])
  })

  it('keeps one timer in StrictMode and cancels it on replacement and unmount', async () => {
    vi.useFakeTimers()
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const props = { applied: 4, revision: 10, available: true, dispatch }
    const first = renderHook(useDesiredControl<number>, {
      initialProps: props,
      wrapper: StrictMode,
    })
    await act(async () => {
      expect(await first.result.current.commit(7)).toBe(true)
    })
    expect(vi.getTimerCount()).toBe(1)
    act(() => vi.advanceTimersByTime(1_000))
    first.rerender({ ...props, available: false })
    expect(first.result.current.phase).toBe('idle')
    expect(vi.getTimerCount()).toBe(0)
    first.rerender(props)
    await act(async () => {
      expect(await first.result.current.commit(8)).toBe(true)
    })
    expect(vi.getTimerCount()).toBe(1)
    act(() => vi.advanceTimersByTime(1_000))
    first.unmount()
    expect(vi.getTimerCount()).toBe(0)

    const remounted = renderHook(useDesiredControl<number>, {
      initialProps: props,
      wrapper: StrictMode,
    })
    await act(async () => {
      expect(await remounted.result.current.commit(9)).toBe(true)
    })
    expect(vi.getTimerCount()).toBe(1)
    act(() => vi.advanceTimersByTime(5_000))
    expect(remounted.result.current).toMatchObject({ desired: 9, phase: 'pending' })
    expect(vi.getTimerCount()).toBe(1)
    remounted.rerender({ ...props, applied: 9, revision: 11 })
    expect(remounted.result.current).toMatchObject({ desired: 9, phase: 'idle' })
    expect(vi.getTimerCount()).toBe(0)
    expect(dispatch.mock.calls).toEqual([[7], [8], [9]])
  })

  it('rolls rejected and unavailable commands back to the latest applied value', async () => {
    let resolveReceipt!: (receipt: { accepted: boolean }) => void
    let rejectReceipt!: (reason: Error) => void
    const dispatch = vi.fn(
      () =>
        new Promise<{ accepted: boolean }>((resolve, reject) => {
          resolveReceipt = resolve
          rejectReceipt = reject
        }),
    )
    const view = renderHook(
      ({ applied, revision }) =>
        useDesiredControl({
          applied,
          revision,
          available: true,
          dispatch,
        }),
      { initialProps: { applied: 4, revision: 10 } },
    )

    act(() => view.result.current.preview(8))
    let rejected!: Promise<boolean>
    act(() => {
      rejected = view.result.current.commit()
    })
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
    act(() => {
      unavailable = view.result.current.commit()
    })
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
    const dispatch = vi.fn(
      () =>
        new Promise<{ accepted: boolean }>((resolve) => {
          resolveReceipt = resolve
        }),
    )
    const view = renderHook(() =>
      useDesiredControl({
        applied: 4,
        revision: 10,
        available: true,
        dispatch,
      }),
    )

    act(() => view.result.current.preview(6))
    act(() => view.result.current.cancel())
    expect(view.result.current).toMatchObject({ desired: 4, phase: 'idle' })
    expect(dispatch).not.toHaveBeenCalled()

    act(() => view.result.current.preview(7))
    let first!: Promise<boolean>
    act(() => {
      first = view.result.current.commit()
    })
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

  it('ignores a late rejection after availability is lost and a newer value is dispatched', async () => {
    let rejectFirstCommand!: (receipt: { accepted: boolean }) => void
    const firstDispatch = vi.fn(
      () =>
        new Promise<{ accepted: boolean }>((resolve) => {
          rejectFirstCommand = resolve
        }),
    )
    const nextDispatch = vi.fn(async () => ({ accepted: true }))
    const { result, rerender } = renderHook(useDesiredControl<number>, {
      initialProps: { applied: 4, revision: 10, available: true, dispatch: firstDispatch },
    })

    let first!: Promise<boolean>
    act(() => {
      result.current.preview(6)
      first = result.current.commit()
    })
    expect(firstDispatch.mock.calls).toEqual([[6]])

    rerender({ applied: 5, revision: 11, available: false, dispatch: nextDispatch })
    expect(result.current).toMatchObject({ desired: 5, phase: 'idle', error: null })
    rerender({ applied: 5, revision: 12, available: true, dispatch: nextDispatch })

    await act(async () => {
      result.current.preview(7)
      expect(await result.current.commit()).toBe(true)
    })
    await act(async () => {
      rejectFirstCommand({ accepted: false })
      expect(await first).toBe(false)
    })

    expect(nextDispatch.mock.calls).toEqual([[7]])
    expect(result.current).toMatchObject({ desired: 7, phase: 'pending', error: null })
  })
})
