import { act, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { StrictMode, useLayoutEffect, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
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
      applied: 4, revision: 10, available: true, dispatch,
    })
    const [trigger, setTrigger] = useState(false)
    const handlePreview = () => {
      preview(7)
      setTrigger(true)
    }
    return (
      <>
        <button type="button" onClick={handlePreview}>Preview</button>
        <output aria-label="Parent control">{phase}: {desired}</output>
        {(!removeChild || !trigger) && <Child cancel={cancel} trigger={trigger} />}
      </>
    )
  }

  render(<Parent />)
  return { dispatch, childCancelled }
}

describe('useDesiredControl', () => {
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
    const { result } = renderHook(() => useDesiredControl({
      applied: 4, revision: 10, available: true, dispatch,
    }), { wrapper: StrictMode })

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
    const { result, unmount } = renderHook(() => useDesiredControl({
      applied: 4, revision: 10, available: true, dispatch,
    }))
    const { commit, preview } = result.current

    let inFlight!: Promise<boolean>
    act(() => { inFlight = commit(7) })
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

  it('keeps accepted false desired pending through stale snapshots until an exact newer snapshot', async () => {
    const dispatch = vi.fn().mockResolvedValue({ accepted: true })
    const view = renderHook(
      ({ applied, revision }) => useDesiredControl({
        applied,
        revision,
        available: true,
        dispatch,
      }),
      { initialProps: { applied: true, revision: 10 } },
    )

    act(() => view.result.current.preview(false))
    expect(view.result.current).toMatchObject({ desired: false, phase: 'preview' })
    await act(async () => { await view.result.current.commit() })
    expect(dispatch).toHaveBeenCalledOnce()
    expect(dispatch).toHaveBeenCalledWith(false)
    expect(view.result.current).toMatchObject({ desired: false, phase: 'pending' })

    view.rerender({ applied: true, revision: 11 })
    expect(view.result.current).toMatchObject({ desired: false, phase: 'pending' })
    view.rerender({ applied: false, revision: 11 })
    expect(view.result.current).toMatchObject({ desired: false, phase: 'idle' })
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

  it('ignores a late rejection after availability is lost and a newer value is dispatched', async () => {
    let rejectFirstCommand!: (receipt: { accepted: boolean }) => void
    const firstDispatch = vi.fn(() => new Promise<{ accepted: boolean }>((resolve) => {
      rejectFirstCommand = resolve
    }))
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
