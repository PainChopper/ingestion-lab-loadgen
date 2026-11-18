import { useCallback, useLayoutEffect, useRef, useState } from 'react'

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
  requestedTps: DesiredControl<number>
  installed: DesiredControl<boolean>
  senderWorkers: DesiredControl<number>
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

function idleState<T>(applied: T, error: string | null = null): DesiredState<T> {
  return { desired: applied, phase: 'idle', submittedRevision: null, error }
}

export function useDesiredControl<T>({
  applied,
  revision,
  available,
  dispatch,
  rejectionMessage = 'Change rejected',
  unavailableMessage = 'Change unavailable',
}: DesiredControlOptions<T>): DesiredControl<T> {
  const [state, setState] = useState(() => idleState(applied))
  const stateRef = useRef<DesiredState<T> | null>(state)
  const optionsRef = useRef({ applied, available, dispatch, revision })

  const acknowledged = state.phase === 'pending' &&
    state.submittedRevision !== null && revision > state.submittedRevision &&
    Object.is(applied, state.desired)
  if (acknowledged || (!available && (state.phase !== 'idle' || state.error !== null))) {
    setState(idleState(applied))
  }

  useLayoutEffect(() => {
    optionsRef.current = { applied, available, dispatch, revision }
  }, [applied, available, dispatch, revision])

  useLayoutEffect(() => {
    stateRef.current = state
  }, [state])

  // Keep the ref live while child layout effects run during updates.
  useLayoutEffect(() => {
    return () => { stateRef.current = null }
  }, [])

  const updateState = useCallback((next: DesiredState<T>) => {
    // A preview and its commit can run in the same event, before React renders.
    stateRef.current = next
    setState(next)
  }, [])

  const preview = useCallback((value: T) => {
    const current = stateRef.current
    if (current === null || !optionsRef.current.available || current.phase === 'pending') return
    updateState({
      desired: value,
      phase: 'preview',
      submittedRevision: null,
      error: null,
    })
  }, [updateState])

  const cancel = useCallback(() => {
    if (stateRef.current?.phase !== 'preview') return
    updateState(idleState(optionsRef.current.applied))
  }, [updateState])

  const commit = useCallback(async (value?: T): Promise<boolean> => {
    const current = stateRef.current
    const options = optionsRef.current
    if (current === null || !options.available || current.phase === 'pending') return false
    const desired = value === undefined
      ? current.phase === 'idle' ? options.applied : current.desired
      : value
    if (Object.is(desired, options.applied)) {
      updateState(idleState(options.applied))
      return true
    }

    const pending: DesiredState<T> = {
      desired, phase: 'pending', submittedRevision: options.revision, error: null,
    }
    updateState(pending)
    try {
      const receipt = await options.dispatch(desired)
      if (!receipt.accepted && stateRef.current === pending) {
        updateState(idleState(optionsRef.current.applied, rejectionMessage))
      }
      return receipt.accepted
    } catch {
      if (stateRef.current === pending) {
        updateState(idleState(optionsRef.current.applied, unavailableMessage))
      }
      return false
    }
  }, [rejectionMessage, unavailableMessage, updateState])

  return {
    applied,
    desired: state.phase === 'idle' ? applied : state.desired,
    phase: state.phase,
    error: state.error,
    available,
    preview,
    cancel,
    commit,
  }
}
