import { useCallback, useLayoutEffect, useRef } from 'react'

const HOLD_DELAY_MS = 420
const HOLD_REPEAT_MS = 120

interface HoldRepeatOptions {
  step: (direction: -1 | 1) => boolean
  onCommit: () => void
  onCancel: () => void
  commitAtLimit?: boolean
}

export function useHoldRepeat({ step, onCommit, onCancel, commitAtLimit = false }: HoldRepeatOptions) {
  const finishRef = useRef<((commit: boolean) => void) | null>(null)
  const optionsRef = useRef({ step, onCommit, onCancel, commitAtLimit })

  useLayoutEffect(() => {
    optionsRef.current = { step, onCommit, onCancel, commitAtLimit }
  }, [step, onCommit, onCancel, commitAtLimit])

  const cancel = useCallback(() => {
    const finish = finishRef.current
    if (finish === null) return false
    finish(false)
    return true
  }, [])

  useLayoutEffect(() => () => {
    cancel()
  }, [cancel])

  const start = (pointerId: number, direction: -1 | 1) => {
    if (finishRef.current !== null || !optionsRef.current.step(direction)) return false

    let delayTimer: number | undefined
    let repeatTimer: number | undefined
    const clearTimers = () => {
      window.clearTimeout(delayTimer)
      window.clearInterval(repeatTimer)
      delayTimer = undefined
      repeatTimer = undefined
    }
    const handleRelease = (event: PointerEvent) => {
      if (event.pointerId === pointerId) finish(true)
    }
    const handlePointerCancel = (event: PointerEvent) => {
      if (event.pointerId === pointerId) finish(false)
    }
    const handleBlur = () => finish(false)
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') finish(false)
    }
    const finish = (commit: boolean) => {
      clearTimers()
      window.removeEventListener('pointerup', handleRelease)
      window.removeEventListener('pointercancel', handlePointerCancel)
      window.removeEventListener('blur', handleBlur)
      window.removeEventListener('keydown', handleKeyDown)
      finishRef.current = null
      if (commit) optionsRef.current.onCommit()
      else optionsRef.current.onCancel()
    }

    finishRef.current = finish
    window.addEventListener('pointerup', handleRelease)
    window.addEventListener('pointercancel', handlePointerCancel)
    window.addEventListener('blur', handleBlur)
    window.addEventListener('keydown', handleKeyDown)
    delayTimer = window.setTimeout(() => {
      repeatTimer = window.setInterval(() => {
        const options = optionsRef.current
        if (options.step(direction)) return
        if (options.commitAtLimit) finish(true)
        else clearTimers()
      }, HOLD_REPEAT_MS)
    }, HOLD_DELAY_MS)
    return true
  }

  return { start, cancel }
}
