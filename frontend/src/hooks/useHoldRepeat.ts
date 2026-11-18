import { useCallback, useEffect, useRef } from 'react'

const HOLD_DELAY_MS = 420
const HOLD_REPEAT_MS = 120

interface HoldRepeatOptions {
  onCommit: () => void
  onCancel: () => void
  commitAtLimit?: boolean
}

export function useHoldRepeat({ onCommit, onCancel, commitAtLimit = false }: HoldRepeatOptions) {
  const finishRef = useRef<((commit: boolean) => void) | null>(null)

  const cancel = useCallback(() => {
    const finish = finishRef.current
    if (finish === null) return false
    finish(false)
    return true
  }, [])

  useEffect(() => () => {
    cancel()
  }, [cancel])

  const start = (pointerId: number, step: () => boolean) => {
    if (finishRef.current !== null || !step()) return false

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
      if (commit) onCommit()
      else onCancel()
    }

    finishRef.current = finish
    window.addEventListener('pointerup', handleRelease)
    window.addEventListener('pointercancel', handlePointerCancel)
    window.addEventListener('blur', handleBlur)
    window.addEventListener('keydown', handleKeyDown)
    delayTimer = window.setTimeout(() => {
      repeatTimer = window.setInterval(() => {
        if (step()) return
        if (commitAtLimit) finish(true)
        else clearTimers()
      }, HOLD_REPEAT_MS)
    }, HOLD_DELAY_MS)
    return true
  }

  return { start, cancel }
}
