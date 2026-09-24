import { Minus, Plus } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import type {
  NumericControlSnapshot,
  ReaderWorkerSlotSnapshot,
  RunState,
  SelectableId,
  SenderWorkerState,
  SenderWorkerSlotSnapshot,
} from '../../model/loadgen'
import type { Point, TextPlacement, WorkerActorBounds } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getWorkerActorLayout,
  type WorkerChipLayout,
} from './workerActorLayout'

export type WorkerActorId = 'reader' | 'sender'

const SENDER_SUCCESS_DURATION_MS = 1_000

interface WorkerActorProps {
  actor: WorkerActorId
  title: string
  titlePoint: TextPlacement
  titleTone: 'reader' | 'sender'
  bounds: WorkerActorBounds
  controls: {
    readonly x: number
    readonly y: number
    readonly width: number
    readonly height: number
  }
  workers: NumericControlSnapshot
  liveWorkers?: number
  drainingWorkers?: number
  workerSlots?: readonly (SenderWorkerSlotSnapshot | ReaderWorkerSlotSnapshot)[] | null
  runState: RunState
  active?: boolean
  inputPort?: Point
  outputPort: Point
  primaryMetric: string
  secondaryMetric?: string
  statusMetric?: string
  metricPoints: {
    readonly primary: TextPlacement
    readonly secondary: TextPlacement
    readonly status: TextPlacement
  }
  orientation?: PipelineOrientation
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
}

function WorkerChip({
  x,
  y,
  scale,
  state,
  slot,
}: WorkerChipLayout & {
  state: SenderWorkerState | ReaderWorkerSlotSnapshot['activity'] | 'active' | 'inactive' | 'success' | 'draining' | 'terminal-error'
  slot?: SenderWorkerSlotSnapshot | ReaderWorkerSlotSnapshot
}) {
  const pinOffsets = [6, 13, 20, 27]
  const chipX = 4
  const chipY = 4

  return (
    <g
      className={`pipeline-worker--${state}`}
      transform={`translate(${x} ${y}) scale(${scale})`}
      data-worker-id={slot?.workerId}
      data-worker-activity={slot?.activity}
      data-worker-lifecycle={slot?.lifecycle}
      data-worker-terminal-error={slot && 'terminalError' in slot ? slot.terminalError : undefined}
    >
      <rect
        x={chipX}
        y={chipY}
        width="31"
        height="31"
        rx="3"
        className="pipeline-worker-chip"
      />
      {pinOffsets.map((offset) => (
        <g key={offset} className="pipeline-worker-chip-pin">
          <line
            x1={chipX + offset}
            y1={chipY - 4}
            x2={chipX + offset}
            y2={chipY}
          />
          <line
            x1={chipX + offset}
            y1={chipY + 31}
            x2={chipX + offset}
            y2={chipY + 35}
          />
          <line
            x1={chipX - 4}
            y1={chipY + offset}
            x2={chipX}
            y2={chipY + offset}
          />
          <line
            x1={chipX + 31}
            y1={chipY + offset}
            x2={chipX + 35}
            y2={chipY + offset}
          />
        </g>
      ))}
      <polyline
        points={`${chipX + 5},${chipY + 17} ${chipX + 10},${chipY + 17} ${chipX + 14},${chipY + 10} ${chipX + 18},${chipY + 23} ${chipX + 22},${chipY + 15} ${chipX + 27},${chipY + 15}`}
        className="pipeline-worker-wave"
      />
      <circle
        cx={chipX + 57}
        cy={chipY + 15.5}
        r="5.5"
        className="pipeline-worker-led"
      />
    </g>
  )
}

export function WorkerActor({
  actor,
  title,
  titlePoint,
  titleTone,
  bounds,
  controls,
  workers,
  liveWorkers,
  drainingWorkers,
  workerSlots,
  runState,
  active,
  inputPort,
  outputPort,
  primaryMetric,
  secondaryMetric,
  statusMetric,
  metricPoints,
  orientation = 'landscape',
  selected,
  onSelect,
  desiredControl,
}: WorkerActorProps) {
  const [recentlySuccessfulWorkerIds, setRecentlySuccessfulWorkerIds] = useState<ReadonlySet<number>>(
    () => new Set(),
  )
  const previousWorkerActivity = useRef<ReadonlyMap<number, SenderWorkerState>>(new Map())
  const successTimeouts = useRef(new Map<number, ReturnType<typeof setTimeout>>())
  const [completedReaderIds, setCompletedReaderIds] = useState<ReadonlySet<number>>(() => new Set())
  const previousReaderActivity = useRef<ReadonlyMap<number, ReaderWorkerSlotSnapshot['activity']>>(new Map())
  const readerTimeouts = useRef(new Map<number, ReturnType<typeof setTimeout>>())
  const holdTimer = useRef<number | null>(null)
  const repeatTimer = useRef<number | null>(null)
  const releaseListeners = useRef<(() => void) | null>(null)
  const holdActive = useRef(false)
  const suppressClick = useRef(false)
  const desiredRef = useRef(desiredControl.desired)
  const commitDesired = desiredControl.commit
  const cancelDesired = desiredControl.cancel
  const previewDesired = desiredControl.preview

  useEffect(() => {
    if (actor !== 'reader' || workerSlots === null || workerSlots === undefined) {
      previousReaderActivity.current = new Map()
      for (const timeout of readerTimeouts.current.values()) clearTimeout(timeout)
      readerTimeouts.current.clear()
      setCompletedReaderIds((current) => current.size === 0 ? current : new Set())
      return
    }

    const nextActivity = new Map<number, ReaderWorkerSlotSnapshot['activity']>()
    const completed: number[] = []
    for (const slot of workerSlots as readonly ReaderWorkerSlotSnapshot[]) {
      nextActivity.set(slot.workerId, slot.activity)
      if (
        (previousReaderActivity.current.get(slot.workerId) === undefined ||
          previousReaderActivity.current.get(slot.workerId) === 'reading' ||
          previousReaderActivity.current.get(slot.workerId) === 'blocked') &&
        slot.activity === 'completed' && slot.lifecycle === 'active'
      ) completed.push(slot.workerId)
    }
    previousReaderActivity.current = nextActivity

    for (const [id, timeout] of readerTimeouts.current) {
      const slot = workerSlots.find((candidate) => candidate.workerId === id)
      if (slot && slot.lifecycle === 'active' && (slot.activity === 'completed' || slot.activity === 'idle')) continue
      clearTimeout(timeout)
      readerTimeouts.current.delete(id)
    }
    setCompletedReaderIds((current) => {
      const visible = new Set([...current].filter((id) => readerTimeouts.current.has(id)))
      return visible.size === current.size ? current : visible
    })

    for (const id of completed) {
      const timeout = setTimeout(() => {
        if (readerTimeouts.current.get(id) !== timeout) return
        readerTimeouts.current.delete(id)
        setCompletedReaderIds((current) => {
          if (!current.has(id)) return current
          const visible = new Set(current)
          visible.delete(id)
          return visible
        })
      }, SENDER_SUCCESS_DURATION_MS)
      readerTimeouts.current.set(id, timeout)
    }
    if (completed.length > 0) {
      setCompletedReaderIds((current) => new Set([...current, ...completed]))
    }
  }, [actor, workerSlots])

  useEffect(() => {
    if (actor !== 'sender' || workerSlots === null || workerSlots === undefined) {
      previousWorkerActivity.current = new Map()
      for (const timeout of successTimeouts.current.values()) clearTimeout(timeout)
      successTimeouts.current.clear()
      setRecentlySuccessfulWorkerIds((current) => current.size === 0 ? current : new Set())
      return
    }

    const nextWorkerActivity = new Map<number, SenderWorkerState>()
    const newlySuccessfulWorkerIds: number[] = []
    for (const slot of workerSlots as readonly SenderWorkerSlotSnapshot[]) {
      nextWorkerActivity.set(slot.workerId, slot.activity)
      if (
        previousWorkerActivity.current.get(slot.workerId) === 'in-flight' &&
        slot.activity === 'idle'
      ) {
        newlySuccessfulWorkerIds.push(slot.workerId)
      }
    }
    previousWorkerActivity.current = nextWorkerActivity

    for (const [workerId, timeout] of successTimeouts.current) {
      if (nextWorkerActivity.has(workerId)) continue
      clearTimeout(timeout)
      successTimeouts.current.delete(workerId)
    }
    setRecentlySuccessfulWorkerIds((current) => {
      const visibleWorkerIds = new Set(
        [...current].filter((workerId) => nextWorkerActivity.has(workerId)),
      )
      return visibleWorkerIds.size === current.size ? current : visibleWorkerIds
    })

    for (const workerId of newlySuccessfulWorkerIds) {
      const previousTimeout = successTimeouts.current.get(workerId)
      if (previousTimeout !== undefined) clearTimeout(previousTimeout)
      const timeout = setTimeout(() => {
        if (successTimeouts.current.get(workerId) !== timeout) return
        successTimeouts.current.delete(workerId)
        setRecentlySuccessfulWorkerIds((visible) => {
          if (!visible.has(workerId)) return visible
          const withoutExpiredWorker = new Set(visible)
          withoutExpiredWorker.delete(workerId)
          return withoutExpiredWorker
        })
      }, SENDER_SUCCESS_DURATION_MS)
      successTimeouts.current.set(workerId, timeout)
    }

    if (newlySuccessfulWorkerIds.length === 0) return
    setRecentlySuccessfulWorkerIds((current) => new Set([
      ...current,
      ...newlySuccessfulWorkerIds,
    ]))
  }, [actor, workerSlots])

  const stopHold = useCallback((commit: boolean) => {
    if (holdTimer.current !== null) window.clearTimeout(holdTimer.current)
    if (repeatTimer.current !== null) window.clearInterval(repeatTimer.current)
    holdTimer.current = null
    repeatTimer.current = null
    releaseListeners.current?.()
    releaseListeners.current = null
    if (!holdActive.current) return
    holdActive.current = false
    if (commit) void commitDesired()
    else cancelDesired()
  }, [cancelDesired, commitDesired])

  useEffect(() => () => stopHold(false), [stopHold])

  useEffect(() => () => {
    for (const timeout of successTimeouts.current.values()) clearTimeout(timeout)
    successTimeouts.current.clear()
    for (const timeout of readerTimeouts.current.values()) clearTimeout(timeout)
    readerTimeouts.current.clear()
  }, [])

  const desiredWorkers = Math.min(
    Math.round(workers.max),
    Math.max(Math.round(workers.min), Math.round(desiredControl.desired)),
  )
  desiredRef.current = desiredWorkers
  const visibleWorkers = workerSlots === undefined ? desiredWorkers : workerSlots?.length ?? 0
  const layout = getWorkerActorLayout(
    actor,
    bounds,
    workers,
    orientation,
    visibleWorkers,
    desiredWorkers,
  )
  const workerMin = Math.round(workers.min)
  const workerMax = Math.round(workers.max)
  const workerStep = Math.max(1, Math.round(workers.step))
  const workerControlDisabled = !desiredControl.available || desiredControl.phase === 'pending'
  const chipState = (index: number): SenderWorkerState | ReaderWorkerSlotSnapshot['activity'] | 'active' | 'inactive' | 'success' | 'draining' | 'terminal-error' => {
    if (workerSlots === undefined || workerSlots === null) {
      return (active ?? (runState === 'running')) ? 'active' : 'inactive'
    }
    const slot = workerSlots[index]
    if (slot === undefined) return 'inactive'
    if ('terminalError' in slot && slot.terminalError) return 'terminal-error'
    if (slot.activity === 'backoff') return 'backoff'
    if (slot.activity === 'blocked') return 'blocked'
    if (slot.lifecycle === 'draining') return 'draining'
    if (actor === 'reader' && completedReaderIds.has(slot.workerId)) return 'success'
    if (slot.activity === 'completed') return 'idle'
    if (slot.activity === 'idle' && recentlySuccessfulWorkerIds.has(slot.workerId)) return 'success'
    return slot.activity
  }
  const actorAriaLabel = actor === 'reader' || liveWorkers === undefined
    ? `Inspect ${actor}`
    : `Inspect ${actor}, ${desiredWorkers} desired, ${liveWorkers} live, ${drainingWorkers ?? 0} draining`
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(actor)
  }
  const stepWorkers = useCallback((delta: number) => {
    const next = Math.min(
      Math.round(workers.max),
      Math.max(Math.round(workers.min), desiredRef.current + delta),
    )
    if (next === desiredRef.current) return false
    desiredRef.current = next
    previewDesired(next)
    return true
  }, [previewDesired, workers.max, workers.min])
  const handleWorkerPointerDown = (
    event: ReactPointerEvent<HTMLButtonElement>,
    delta: number,
  ) => {
    event.preventDefault()
    if (!desiredControl.available) return
    stopHold(false)
    if (desiredControl.phase === 'pending' || !stepWorkers(delta)) return
    holdActive.current = true
    suppressClick.current = true
    const release = () => stopHold(true)
    const cancel = () => stopHold(false)
    const cancelKey = (keyboardEvent: globalThis.KeyboardEvent) => {
      if (keyboardEvent.key === 'Escape') cancel()
    }
    releaseListeners.current = () => {
      window.removeEventListener('pointerup', release)
      window.removeEventListener('pointercancel', cancel)
      window.removeEventListener('blur', cancel)
      window.removeEventListener('keydown', cancelKey)
    }
    window.addEventListener('pointerup', release)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('blur', cancel)
    window.addEventListener('keydown', cancelKey)
    event.currentTarget.setPointerCapture?.(event.pointerId)
    holdTimer.current = window.setTimeout(() => {
      repeatTimer.current = window.setInterval(() => {
        if (!stepWorkers(delta)) stopHold(true)
      }, 120)
    }, 420)
  }
  const handleWorkerClick = (delta: number) => {
    if (!desiredControl.available) return
    if (suppressClick.current) {
      suppressClick.current = false
      return
    }
    if (desiredControl.phase === 'pending' || !stepWorkers(delta)) return
    void desiredControl.commit()
  }

  return (
    <>
      <text
        x={titlePoint.x}
        y={titlePoint.y}
        textAnchor={titlePoint.anchor}
        className={`pipeline-title pipeline-title--${titleTone}`}
      >
        {title}
      </text>
      <foreignObject
        x={controls.x}
        y={controls.y}
        width={controls.width}
        height={controls.height}
        className="pipeline-worker-control"
      >
        <div
          className="pipeline-worker-control__body"
          aria-label={`${title.toLocaleLowerCase()} workers`}
        >
          <button
            id={`${actor}-minus`}
            type="button"
            onPointerDown={(event) => handleWorkerPointerDown(event, -workerStep)}
            onLostPointerCapture={() => stopHold(false)}
            onClick={() => handleWorkerClick(-workerStep)}
            disabled={workerControlDisabled || desiredWorkers <= workerMin}
            title={`Remove ${actor} worker`}
            aria-label={`Remove ${actor} worker`}
          >
            <Minus aria-hidden="true" />
          </button>
          <output
            id={`${actor}-count`}
            aria-label={
              desiredControl.phase === 'idle'
                ? `${title} worker count, ${desiredWorkers} applied`
                : `${title} worker count, ${workers.applied ?? 0} applied, ${desiredWorkers} ${desiredControl.phase}`
            }
          >
            {desiredWorkers}
          </output>
          <button
            id={`${actor}-plus`}
            type="button"
            onPointerDown={(event) => handleWorkerPointerDown(event, workerStep)}
            onLostPointerCapture={() => stopHold(false)}
            onClick={() => handleWorkerClick(workerStep)}
            disabled={workerControlDisabled || desiredWorkers >= workerMax}
            title={`Add ${actor} worker`}
            aria-label={`Add ${actor} worker`}
          >
            <Plus aria-hidden="true" />
          </button>
        </div>
      </foreignObject>
      <g
        id={`${actor}-actor`}
        className={`pipeline-actor pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
        role="button"
        tabIndex={0}
        aria-label={actorAriaLabel}
        aria-pressed={selected}
        data-worker-count={desiredWorkers}
        data-worker-layout={layout.mode}
        data-worker-columns={layout.columns}
        data-worker-rows={layout.rows}
        onClick={() => onSelect(actor)}
        onKeyDown={handleKeyDown}
      >
        <rect
          x={bounds.x}
          y={layout.top}
          width={bounds.width}
          height={layout.height}
          rx="5"
          className="pipeline-actor-box"
        />
        {layout.chips.filter((_, index) => workerSlots === undefined || workerSlots?.length === 0 || (workerSlots !== null && index < workerSlots.length)).map((chip, index) => (
          <WorkerChip
            key={workerSlots?.[index]?.workerId ?? index}
            {...chip}
            state={chipState(index)}
            slot={workerSlots?.[index]}
          />
        ))}
        {inputPort && (
          <circle
            cx={inputPort.x}
            cy={inputPort.y}
            r="7"
            className="pipeline-port"
          />
        )}
        <circle
          cx={outputPort.x}
          cy={outputPort.y}
          r="7"
          className="pipeline-port"
        />
        <text
          x={metricPoints.primary.x}
          y={metricPoints.primary.y}
          textAnchor={metricPoints.primary.anchor}
          className="pipeline-value pipeline-worker-primary"
        >
          {primaryMetric}
        </text>
        {secondaryMetric && (
          <text
            x={metricPoints.secondary.x}
            y={metricPoints.secondary.y}
            textAnchor={metricPoints.secondary.anchor}
            className="pipeline-small pipeline-worker-secondary"
          >
            {secondaryMetric}
          </text>
        )}
        {statusMetric && (
          <text
            x={metricPoints.status.x}
            y={metricPoints.status.y}
            textAnchor={metricPoints.status.anchor}
            className="pipeline-worker-status"
          >
            {statusMetric}
          </text>
        )}
      </g>
    </>
  )
}
