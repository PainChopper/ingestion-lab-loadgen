import { useCallback, useEffect, useRef, useState } from 'react'
import type { KeyboardEvent, PointerEvent as ReactPointerEvent } from 'react'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import { VALVE_INSTALLATION_CONTROL } from './throttlerValve'

export function useValveInstallation(
  appliedInstalled: boolean | null,
  installedControl?: DesiredControl<boolean>,
) {
  const [installationDragProgress, setInstallationDragProgress] = useState(0)
  const previousAppliedMode = useRef(appliedInstalled)
  const installationControlRef = useRef<SVGGElement>(null)
  const installationFocusRequested = useRef(false)
  const installationDragProgressRef = useRef(0)
  const suppressInstallationClick = useRef(false)
  const installationDrag = useRef<{
    startX: number
    startY: number
    moved: boolean
  } | null>(null)
  const cancelInstalled = installedControl?.cancel
  const previewInstalled = installedControl?.preview
  const commitInstalled = installedControl?.commit

  const cancelInstallationPreview = useCallback(() => {
    installationDrag.current = null
    installationDragProgressRef.current = 0
    cancelInstalled?.()
    setInstallationDragProgress(0)
  }, [cancelInstalled])

  const cancelInstallationDrag = useCallback(() => {
    if (installationDrag.current === null) return false
    suppressInstallationClick.current = true
    cancelInstallationPreview()
    return true
  }, [cancelInstallationPreview])

  useEffect(() => {
    if (cancelInstalled === undefined) return
    const handleWindowBlur = () => cancelInstallationPreview()
    const handleWindowKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape' && !event.defaultPrevented && cancelInstallationDrag()) {
        event.preventDefault()
      }
    }
    window.addEventListener('blur', handleWindowBlur)
    window.addEventListener('keydown', handleWindowKeyDown)
    return () => {
      window.removeEventListener('blur', handleWindowBlur)
      window.removeEventListener('keydown', handleWindowKeyDown)
    }
  }, [cancelInstallationDrag, cancelInstallationPreview, cancelInstalled])

  useEffect(() => {
    if (previousAppliedMode.current === appliedInstalled) return
    previousAppliedMode.current = appliedInstalled
    cancelInstallationPreview()
    if (installationFocusRequested.current) {
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    }
  }, [cancelInstallationPreview, appliedInstalled])

  const installationCandidate =
    installedControl === undefined || installedControl.phase === 'idle'
      ? null
      : installedControl.desired
  const installationCandidateKind =
    installedControl === undefined || installedControl.phase === 'idle'
      ? null
      : installedControl.phase
  const targetInstalled = !appliedInstalled
  const installationError = installedControl?.error
  const installationControlAvailable =
    appliedInstalled !== null &&
    installedControl !== undefined &&
    installedControl.available &&
    installedControl.phase !== 'pending'

  const requestInstalled = useCallback(() => {
    if (!installationControlAvailable) return
    installationFocusRequested.current = true
    previewInstalled?.(targetInstalled)
    void commitInstalled?.(targetInstalled).then((accepted) => {
      if (accepted) return
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    })
  }, [installationControlAvailable, commitInstalled, previewInstalled, targetInstalled])

  const handleInstallationPointerDown = (event: ReactPointerEvent<SVGGElement>) => {
    event.preventDefault()
    event.stopPropagation()
    if (!installationControlAvailable) return
    suppressInstallationClick.current = false
    installationDrag.current = {
      startX: event.clientX,
      startY: event.clientY,
      moved: false,
    }
    previewInstalled?.(targetInstalled)
    installationDragProgressRef.current = 0
    setInstallationDragProgress(0)
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }

  const handleInstallationPointerMove = (event: ReactPointerEvent<SVGGElement>) => {
    const drag = installationDrag.current
    if (drag === null) return
    event.preventDefault()
    event.stopPropagation()
    const dx = event.clientX - drag.startX
    const dy = event.clientY - drag.startY
    const projection =
      appliedInstalled === false
        ? Math.max(dy, (-dx + dy) / Math.sqrt(2))
        : Math.max(-dy, (dx - dy) / Math.sqrt(2))
    if (Math.hypot(dx, dy) >= 4) drag.moved = true
    const progress = Math.min(
      1,
      Math.max(0, projection / VALVE_INSTALLATION_CONTROL.dragThresholdPx),
    )
    installationDragProgressRef.current = progress
    setInstallationDragProgress(progress)
  }

  const handleInstallationPointerUp = (event: ReactPointerEvent<SVGGElement>) => {
    const drag = installationDrag.current
    if (drag === null) return
    event.preventDefault()
    event.stopPropagation()
    installationDrag.current = null
    event.currentTarget.releasePointerCapture?.(event.pointerId)
    if (!drag.moved) return
    suppressInstallationClick.current = true
    if (installationDragProgressRef.current >= 1) {
      requestInstalled()
    } else {
      cancelInstallationPreview()
    }
  }

  const handleInstallationClick = () => {
    if (suppressInstallationClick.current) {
      suppressInstallationClick.current = false
      return
    }
    requestInstalled()
  }

  const handleInstallationKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key === 'Escape') {
      if (cancelInstallationDrag()) {
        event.preventDefault()
        event.stopPropagation()
      }
      return
    }
    if ((event.key !== 'Enter' && event.key !== ' ') || event.repeat) return
    event.preventDefault()
    event.stopPropagation()
    requestInstalled()
  }

  return {
    installationCandidate,
    installationCandidateKind,
    installationControlAvailable,
    installationControlRef,
    installationDragProgress,
    installationError,
    targetInstalled,
    cancelInstallationPreview,
    handleInstallationPointerDown,
    handleInstallationPointerMove,
    handleInstallationPointerUp,
    handleInstallationClick,
    handleInstallationKeyDown,
  }
}
