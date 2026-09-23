import { useCallback, useEffect, useRef, useState } from 'react'
import type { ThrottlerInstallationMode } from '../model/loadgen'

export type DesiredControlPhase = 'idle' | 'preview' | 'pending'

export interface DesiredControl<T> {
  readonly applied: T
  readonly desired: T
  readonly phase: DesiredControlPhase
  readonly error: string | null
  readonly available: boolean
  readonly preview: (value: T) => void
  readonly cancel: () => void
  readonly commit: (value?: T) => Promise<boolean>
}

export interface LiveControls {
  readerWorkers: DesiredControl<number>
  readBatchSize: DesiredControl<number>
  requestedTps: DesiredControl<number>
  installationMode: DesiredControl<ThrottlerInstallationMode>
  senderWorkers: DesiredControl<number>
  simulatedDelayMs: DesiredControl<number>
  simulatedErrorRatePercent: DesiredControl<number>
  timeoutMs: DesiredControl<number>
}

interface DesiredControlOptions<T> {
  readonly applied: T
  readonly revision: number
  readonly available: boolean
  readonly dispatch: (value: T) => Promise<{ readonly accepted: boolean }>
  readonly rejectionMessage?: string
  readonly unavailableMessage?: string
}

interface DesiredState<T> {
  readonly desired: T
  readonly phase: DesiredControlPhase
  readonly submittedRevision: number | null
  readonly error: string | null
}

export function useDesiredControl<T>({
  applied,
  revision,
  available,
  dispatch,
  rejectionMessage = 'Change rejected',
  unavailableMessage = 'Change unavailable',
}: DesiredControlOptions<T>): DesiredControl<T> {
  const [state, setState] = useState<DesiredState<T>>(() => ({
    desired: applied,
    phase: 'idle',
    submittedRevision: null,
    error: null,
  }))
  const stateRef = useRef(state)
  const appliedRef = useRef(applied)
  const revisionRef = useRef(revision)
  const availableRef = useRef(available)
  const dispatchRef = useRef(dispatch)
  const tokenRef = useRef(0)

  stateRef.current = state
  appliedRef.current = applied
  revisionRef.current = revision
  availableRef.current = available
  dispatchRef.current = dispatch

  useEffect(() => {
    setState((current) => {
      if (!available) {
        if (current.phase !== 'idle') tokenRef.current += 1
        return Object.is(current.desired, applied) &&
          current.phase === 'idle' && current.error === null
          ? current
          : {
              desired: applied,
              phase: 'idle',
              submittedRevision: null,
              error: null,
            }
      }

      if (current.phase === 'idle') {
        return Object.is(current.desired, applied)
          ? current
          : { ...current, desired: applied }
      }
      if (
        current.phase === 'pending' &&
        current.submittedRevision !== null &&
        revision > current.submittedRevision &&
        Object.is(applied, current.desired)
      ) {
        tokenRef.current += 1
        return {
          desired: applied,
          phase: 'idle',
          submittedRevision: null,
          error: null,
        }
      }
      return current
    })
  }, [applied, available, revision])

  const preview = useCallback((value: T) => {
    if (!availableRef.current || stateRef.current.phase === 'pending') return
    const next: DesiredState<T> = {
      desired: value,
      phase: 'preview',
      submittedRevision: null,
      error: null,
    }
    stateRef.current = next
    setState(next)
  }, [])

  const cancel = useCallback(() => {
    if (stateRef.current.phase !== 'preview') return
    tokenRef.current += 1
    const next: DesiredState<T> = {
      desired: appliedRef.current,
      phase: 'idle',
      submittedRevision: null,
      error: null,
    }
    stateRef.current = next
    setState(next)
  }, [])

  const commit = useCallback(async (value?: T): Promise<boolean> => {
    const current = stateRef.current
    if (!availableRef.current || current.phase === 'pending') return false
    const desired = value === undefined ? current.desired : value
    if (Object.is(desired, appliedRef.current)) {
      setState({
        desired: appliedRef.current,
        phase: 'idle',
        submittedRevision: null,
        error: null,
      })
      return true
    }

    const token = tokenRef.current + 1
    tokenRef.current = token
    const submittedRevision = revisionRef.current
    const pending: DesiredState<T> = {
      desired, phase: 'pending', submittedRevision, error: null,
    }
    stateRef.current = pending
    setState(pending)
    try {
      const receipt = await dispatchRef.current(desired)
      if (tokenRef.current !== token) return receipt.accepted
      if (receipt.accepted) return true
      setState({
        desired: appliedRef.current,
        phase: 'idle',
        submittedRevision: null,
        error: rejectionMessage,
      })
      return false
    } catch {
      if (tokenRef.current === token) {
        setState({
          desired: appliedRef.current,
          phase: 'idle',
          submittedRevision: null,
          error: unavailableMessage,
        })
      }
      return false
    }
  }, [rejectionMessage, unavailableMessage])

  return {
    applied,
    desired: state.desired,
    phase: state.phase,
    error: state.error,
    available,
    preview,
    cancel,
    commit,
  }
}
