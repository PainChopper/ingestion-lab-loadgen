import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react'
import type {
  CSSProperties,
  KeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react'
import type {
  QueueSnapshot,
  SelectableId,
  ThrottlerInstallationMode,
  ThrottlerSnapshot,
} from '../../model/loadgen'
import { queuePressureColor } from '../../model/queueFlowState'
import { formatRate } from './formatters'
import { ACTOR_GEOMETRY, type PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  getValveWheelKnobs,
  getValveTargets,
  nextWheelPhase,
  openingPercent,
  VALVE_APERTURE,
  VALVE_DETACHED_ASSEMBLY,
  VALVE_FLANGES,
  VALVE_INSTALLATION_CONTROL,
  VALVE_PISTON,
  valvePistonCenterY,
  valueToOpeningIndex,
  valveIsAdjustable,
} from './throttlerValve'

interface ThrottlerActorProps {
  snapshot: ThrottlerSnapshot
  upstreamQueue: QueueSnapshot
  previewTps: number | null
  selected: boolean
  onSelect: (id: SelectableId) => void
  onPreviewTpsChange: (value: number | null) => void
  onRequestedTpsChange: (value: number) => Promise<boolean>
  onInstallationModeChange: (
    value: ThrottlerInstallationMode,
  ) => Promise<boolean>
  geometry?: PipelineGeometry['actors']['throttler']
  orientation?: PipelineOrientation
}

const HOLD_DELAY_MS = 420
const HOLD_REPEAT_MS = 120
function flowColor(queue: QueueSnapshot): string {
  if (queue.flowState === 'connection-error') return 'var(--red)'
  if (queue.flowState === 'stopped') return 'var(--muted)'
  return queuePressureColor(queue.displayedPressure)
}

export function ThrottlerActor({
  snapshot,
  upstreamQueue,
  previewTps,
  selected,
  onSelect,
  onPreviewTpsChange,
  onRequestedTpsChange,
  onInstallationModeChange,
  geometry,
  orientation = 'landscape',
}: ThrottlerActorProps) {
  const localGeometry = ACTOR_GEOMETRY.throttler
  const resolvedGeometry = geometry ?? {
    ...localGeometry,
    transform: { x: 0, y: 0 },
  }
  const portrait = orientation === 'portrait'
  const actorTransform = resolvedGeometry.transform.x === 0 &&
      resolvedGeometry.transform.y === 0
    ? undefined
    : `translate(${resolvedGeometry.transform.x} ${resolvedGeometry.transform.y})`
  const titlePoint = {
    x: resolvedGeometry.title.x - resolvedGeometry.transform.x,
    y: resolvedGeometry.title.y - resolvedGeometry.transform.y,
  }
  const targets = getValveTargets(snapshot.requestedTps)
  const adjustable = valveIsAdjustable(snapshot.requestedTps)
  const appliedIndex = valueToOpeningIndex(
    snapshot.requestedTps.applied,
    snapshot.requestedTps,
    targets,
  )
  const snapshotCandidate = snapshot.requestedTps.preview ??
    snapshot.requestedTps.pending
  const candidateValue = previewTps ?? snapshotCandidate
  const candidateIndex = candidateValue === null
    ? null
    : valueToOpeningIndex(candidateValue, snapshot.requestedTps, targets)
  const candidateKind = previewTps !== null || snapshot.requestedTps.preview !== null
    ? 'preview'
    : snapshot.requestedTps.pending !== null
      ? 'pending'
      : null
  const [confirmedWheelPhase, setConfirmedWheelPhase] = useState(0)
  const [candidateWheelPhase, setCandidateWheelPhase] = useState<number | null>(null)
  const [installationPreview, setInstallationPreview] =
    useState<ThrottlerInstallationMode | null>(null)
  const [installationDragProgress, setInstallationDragProgress] = useState(0)
  const [installationError, setInstallationError] = useState<string | null>(null)
  const previousAppliedIndex = useRef(appliedIndex)
  const previousAppliedMode = useRef(snapshot.installationMode.applied)
  const holdTimer = useRef<number | null>(null)
  const repeatTimer = useRef<number | null>(null)
  const releaseListeners = useRef<(() => void) | null>(null)
  const candidateIndexRef = useRef(candidateIndex ?? appliedIndex)
  const wheelPhaseRef = useRef(confirmedWheelPhase)
  const targetsRef = useRef(targets)
  const adjustableRef = useRef(adjustable)
  const installationControlRef = useRef<SVGGElement>(null)
  const installationCommandActive = useRef(false)
  const installationFocusRequested = useRef(false)
  const installationDragProgressRef = useRef(0)
  const suppressInstallationClick = useRef(false)
  const installationDrag = useRef<{
    startX: number
    startY: number
    moved: boolean
  } | null>(null)

  const derivedCandidatePhase = candidateIndex === null
    ? confirmedWheelPhase
    : nextWheelPhase(
      confirmedWheelPhase,
      candidateIndex - appliedIndex,
    )
  const wheelPhase = candidateWheelPhase ?? derivedCandidatePhase
  const wheelKnobs = getValveWheelKnobs(wheelPhase)
  const backKnobs = wheelKnobs.filter((knob) => knob.layer === 'back')
  const frontKnobs = wheelKnobs.filter((knob) => knob.layer === 'front')
  candidateIndexRef.current = candidateIndex ?? appliedIndex
  wheelPhaseRef.current = wheelPhase
  targetsRef.current = targets
  adjustableRef.current = adjustable

  useEffect(() => {
    const previous = previousAppliedIndex.current
    if (previous !== appliedIndex) {
      setConfirmedWheelPhase((phase) =>
        nextWheelPhase(phase, appliedIndex - previous)
      )
      setCandidateWheelPhase(null)
      previousAppliedIndex.current = appliedIndex
    }
  }, [appliedIndex])

  useEffect(() => {
    if (candidateValue === null) setCandidateWheelPhase(null)
  }, [candidateValue])

  const cancelInstallationPreview = useCallback(() => {
    installationDrag.current = null
    installationDragProgressRef.current = 0
    setInstallationPreview(null)
    setInstallationDragProgress(0)
  }, [])

  useEffect(() => {
    const handleWindowBlur = () => cancelInstallationPreview()
    window.addEventListener('blur', handleWindowBlur)
    return () => window.removeEventListener('blur', handleWindowBlur)
  }, [cancelInstallationPreview])

  useEffect(() => {
    if (previousAppliedMode.current === snapshot.installationMode.applied) return
    previousAppliedMode.current = snapshot.installationMode.applied
    cancelInstallationPreview()
    if (installationFocusRequested.current) {
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    }
  }, [cancelInstallationPreview, snapshot.installationMode.applied])

  const stopHold = useCallback(() => {
    if (holdTimer.current !== null) window.clearTimeout(holdTimer.current)
    if (repeatTimer.current !== null) window.clearInterval(repeatTimer.current)
    holdTimer.current = null
    repeatTimer.current = null
    releaseListeners.current?.()
    releaseListeners.current = null
  }, [])

  useEffect(() => stopHold, [stopHold])

  const requestDelta = useCallback((delta: number) => {
    const availableTargets = targetsRef.current
    if (!adjustableRef.current || availableTargets === null) return false

    const sourceIndex = candidateIndexRef.current
    const targetIndex = Math.min(11, Math.max(0, sourceIndex + delta))
    const actualDelta = targetIndex - sourceIndex
    if (actualDelta === 0) {
      stopHold()
      return false
    }

    const targetTps = availableTargets[targetIndex]
    const targetPhase = nextWheelPhase(wheelPhaseRef.current, actualDelta)
    candidateIndexRef.current = targetIndex
    wheelPhaseRef.current = targetPhase
    setCandidateWheelPhase(targetPhase)
    onPreviewTpsChange(targetTps)
    void onRequestedTpsChange(targetTps).then((accepted) => {
      if (!accepted) {
        onPreviewTpsChange(null)
        setCandidateWheelPhase(null)
      }
    })
    return true
  }, [onPreviewTpsChange, onRequestedTpsChange, stopHold])

  const handlePointerDown = (
    event: ReactPointerEvent<SVGRectElement>,
    delta: -1 | 1,
  ) => {
    event.preventDefault()
    event.stopPropagation()
    stopHold()
    if (!requestDelta(delta)) return

    const release = () => stopHold()
    releaseListeners.current = () => {
      window.removeEventListener('pointerup', release)
      window.removeEventListener('pointercancel', release)
      window.removeEventListener('blur', release)
    }
    window.addEventListener('pointerup', release)
    window.addEventListener('pointercancel', release)
    window.addEventListener('blur', release)
    holdTimer.current = window.setTimeout(() => {
      repeatTimer.current = window.setInterval(() => {
        requestDelta(delta)
      }, HOLD_REPEAT_MS)
    }, HOLD_DELAY_MS)
  }

  const handleSelectKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  const handleValveKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    const changes: Partial<Record<string, number>> = {
      ArrowLeft: -1,
      ArrowDown: -1,
      ArrowRight: 1,
      ArrowUp: 1,
      PageDown: -5,
      PageUp: 5,
      Home: -11,
      End: 11,
    }
    const delta = changes[event.key]
    if (delta === undefined) return
    event.preventDefault()
    event.stopPropagation()
    requestDelta(delta)
  }

  const appliedInstallationMode = snapshot.installationMode.applied
  const installationCandidate = installationPreview ??
    snapshot.installationMode.pending
  const installationCandidateKind = installationPreview !== null
    ? 'preview'
    : snapshot.installationMode.pending !== null
      ? 'pending'
      : null
  const targetInstallationMode: ThrottlerInstallationMode =
    appliedInstallationMode === 'bypass' ? 'installed' : 'bypass'
  const installationControlAvailable =
    appliedInstallationMode !== null &&
    snapshot.installationMode.writable &&
    snapshot.installationMode.applyMode !== 'unavailable' &&
    snapshot.installationMode.pending === null

  const requestInstallationMode = useCallback(() => {
    if (!installationControlAvailable || installationCommandActive.current) {
      return
    }
    installationCommandActive.current = true
    installationFocusRequested.current = true
    setInstallationError(null)
    setInstallationPreview(targetInstallationMode)
    void onInstallationModeChange(targetInstallationMode).then((accepted) => {
      installationCommandActive.current = false
      if (accepted) {
        cancelInstallationPreview()
        return
      }
      cancelInstallationPreview()
      setInstallationError('Valve mode change rejected')
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    }).catch(() => {
      installationCommandActive.current = false
      cancelInstallationPreview()
      setInstallationError('Valve mode change unavailable')
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    })
  }, [
    cancelInstallationPreview,
    installationControlAvailable,
    onInstallationModeChange,
    targetInstallationMode,
  ])

  const handleInstallationPointerDown = (
    event: ReactPointerEvent<SVGGElement>,
  ) => {
    event.preventDefault()
    event.stopPropagation()
    if (!installationControlAvailable) return
    suppressInstallationClick.current = false
    installationDrag.current = {
      startX: event.clientX,
      startY: event.clientY,
      moved: false,
    }
    setInstallationError(null)
    setInstallationPreview(targetInstallationMode)
    installationDragProgressRef.current = 0
    setInstallationDragProgress(0)
    event.currentTarget.setPointerCapture?.(event.pointerId)
  }

  const handleInstallationPointerMove = (
    event: ReactPointerEvent<SVGGElement>,
  ) => {
    const drag = installationDrag.current
    if (drag === null) return
    event.preventDefault()
    event.stopPropagation()
    const dx = event.clientX - drag.startX
    const dy = event.clientY - drag.startY
    const projection = appliedInstallationMode === 'bypass'
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

  const handleInstallationPointerUp = (
    event: ReactPointerEvent<SVGGElement>,
  ) => {
    const drag = installationDrag.current
    if (drag === null) return
    event.preventDefault()
    event.stopPropagation()
    installationDrag.current = null
    event.currentTarget.releasePointerCapture?.(event.pointerId)
    if (!drag.moved) return
    suppressInstallationClick.current = true
    if (installationDragProgressRef.current >= 1) {
      requestInstallationMode()
    } else {
      cancelInstallationPreview()
    }
  }

  const handleInstallationClick = () => {
    if (suppressInstallationClick.current) {
      suppressInstallationClick.current = false
      return
    }
    requestInstallationMode()
  }

  const handleInstallationKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      cancelInstallationPreview()
      return
    }
    if (event.key !== 'Enter' && event.key !== ' ' || event.repeat) return
    event.preventDefault()
    event.stopPropagation()
    requestInstallationMode()
  }

  const appliedGateY = valvePistonCenterY(appliedIndex)
  const candidateGateY = candidateIndex === null
    ? null
    : valvePistonCenterY(candidateIndex ?? appliedIndex)
  const candidateState = candidateKind === null ? 'applied' : candidateKind
  const rangeIndex = candidateIndex ?? appliedIndex
  const rangeText = `${openingPercent(rangeIndex)}% open, ${candidateState}; applied ${openingPercent(appliedIndex)}% open`
  const valveStyle = {
    '--pipeline-valve-flow-color': flowColor(upstreamQueue),
  } as CSSProperties
  const apertureClipId = 'throttler-valve-aperture-clip'

  const renderGate = (
    gateY: number,
    className: string,
    kind: 'applied' | 'preview' | 'pending',
  ) => (
    <g
      className={className}
      clipPath={`url(#${apertureClipId})`}
      data-gate-kind={kind}
      data-gate-y={gateY.toFixed(3)}
      data-piston-axis-ratio={(
        VALVE_PISTON.radiusY / VALVE_PISTON.radiusX
      ).toFixed(2)}
    >
      <line x1="430" y1="395" x2="430" y2={gateY} />
      <ellipse
        cx="430"
        cy={gateY}
        rx={VALVE_PISTON.radiusX}
        ry={VALVE_PISTON.radiusY}
      />
    </g>
  )

  const renderKnob = (knob: (typeof wheelKnobs)[number], index: number) => (
    <circle
      key={`${knob.angle}-${index}`}
      cx={knob.x}
      cy={knob.y}
      r="2.5"
      className={`pipeline-valve-wheel-knob pipeline-valve-wheel-knob--${knob.layer}`}
      data-orbit-angle={knob.angle}
      data-orbit-depth={knob.depth}
      data-orbit-layer={knob.layer}
    />
  )

  const renderWheel = (className: string, transform?: string) => (
    <g className={className} transform={transform} data-wheel-phase={wheelPhase}>
      <g className="pipeline-valve-wheel-knobs pipeline-valve-wheel-knobs--back">
        {backKnobs.map(renderKnob)}
      </g>
      <ellipse cx="430" cy="350" rx="27" ry="10.5" className="pipeline-valve-wheel-rim" />
      <ellipse cx="430" cy="350" rx="20.5" ry="7" className="pipeline-valve-wheel-inner-rim" />
      <g transform="translate(430 350) scale(1 0.42)">
        <g
          className="pipeline-valve-wheel-inner-motion"
          style={{ transform: `rotate(${wheelPhase * 60}deg)` }}
          data-internal-phase={wheelPhase}
        >
          {Array.from({ length: 5 }, (_, index) => {
            const angle = index * 72 * Math.PI / 180
            return (
              <line
                key={index}
                x1="0"
                y1="0"
                x2={Math.cos(angle) * 19}
                y2={Math.sin(angle) * 19}
                className="pipeline-valve-wheel-spoke"
              />
            )
          })}
          <circle cx="17" cy="0" r="2.2" className="pipeline-valve-wheel-index" />
        </g>
        <circle r="5.2" className="pipeline-valve-wheel-hub" />
      </g>
      <g className="pipeline-valve-wheel-knobs pipeline-valve-wheel-knobs--front">
        {frontKnobs.map(renderKnob)}
      </g>
    </g>
  )

  const detachedTransform = [
    `translate(${VALVE_DETACHED_ASSEMBLY.translateX} ${VALVE_DETACHED_ASSEMBLY.translateY})`,
    `rotate(${VALVE_DETACHED_ASSEMBLY.rotationDegrees} 430 350)`,
  ].join(' ')
  const renderDetachedAssembly = (className: string) => (
    <g
      className={className}
      transform={detachedTransform}
      data-detached-anchor={`${VALVE_DETACHED_ASSEMBLY.wheelCenterX} ${VALVE_DETACHED_ASSEMBLY.wheelCenterY}`}
    >
      <g className={className.includes('--applied')
        ? 'pipeline-valve-detached-sway'
        : undefined}>
        {renderWheel('pipeline-valve-wheel')}
        <rect x="424" y="363" width="12" height="12" rx="1" className="pipeline-valve-neck" />
        <line x1="430" y1="375" x2="430" y2="405" className="pipeline-valve-stem" />
        <ellipse
          cx="430"
          cy="409"
          rx={VALVE_PISTON.radiusX}
          ry={VALVE_PISTON.radiusY}
          className="pipeline-valve-detached-piston"
        />
      </g>
    </g>
  )

  const installationStatus = installationCandidate === null
    ? `${appliedInstallationMode ?? 'unavailable'} applied`
    : `${installationCandidate} ${installationCandidateKind}; ${appliedInstallationMode ?? 'unavailable'} applied`

  return (
    <g transform={actorTransform} data-pipeline-orientation={orientation}>
      <text
        x={titlePoint.x}
        y={titlePoint.y}
        textAnchor="middle"
        className="pipeline-title pipeline-title--throttler"
      >
        THROTTLER
      </text>
      <g id="throttler-actor" className="pipeline-actor" style={valveStyle}>
        <defs>
          <clipPath id={apertureClipId}>
            <ellipse
              cx="430"
              cy={VALVE_APERTURE.centerY}
              rx={VALVE_APERTURE.radiusX}
              ry={VALVE_APERTURE.radiusY}
            />
          </clipPath>
        </defs>
        <g
          className={`pipeline-selectable${selected ? ' pipeline-selectable--selected' : ''}`}
          role="button"
          tabIndex={0}
          aria-label="Inspect throttler"
          aria-pressed={selected}
          onClick={() => onSelect(snapshot.id)}
          onKeyDown={handleSelectKeyDown}
        >
          <rect
            x={localGeometry.bounds.x}
            y={localGeometry.bounds.y}
            width={localGeometry.bounds.width}
            height={localGeometry.bounds.height}
            rx="5"
            className="pipeline-actor-box"
          />
        </g>
        {portrait ? (
          <>
            <path
              d={`M430 282 H382 Q374 282 374 290 V407 Q374 415 382 415 H${VALVE_FLANGES.left}`}
              className="pipeline-valve-flow-line"
              fill="none"
              data-connector-side="portrait-input"
            />
            <path
              d={`M${VALVE_FLANGES.right} 415 H478 Q486 415 486 423 V467 Q486 475 478 475 H430`}
              className="pipeline-valve-flow-line"
              fill="none"
              data-connector-side="portrait-output"
            />
            <circle cx="430" cy="282" r="7" className="pipeline-port" />
            <circle cx="430" cy="475" r="7" className="pipeline-port" />
          </>
        ) : (
          <>
            <line
              x1="355"
              y1="415"
              x2={VALVE_FLANGES.left}
              y2="415"
              className="pipeline-valve-flow-line"
              data-connector-side="left"
            />
            <line
              x1={VALVE_FLANGES.right}
              y1="415"
              x2="505"
              y2="415"
              className="pipeline-valve-flow-line"
              data-connector-side="right"
            />
            <circle cx="355" cy="415" r="7" className="pipeline-port" />
            <circle cx="505" cy="415" r="7" className="pipeline-port" />
          </>
        )}

        {appliedInstallationMode === 'installed' && (
          <>
            <text x="382" y="304" textAnchor="middle" className="pipeline-small">
              Requested TPS
            </text>
            <text id="requested-display" x="382" y="321" textAnchor="middle" className="pipeline-value">
              {formatRate(snapshot.requestedTps.applied)}
            </text>
          </>
        )}
        <text x={appliedInstallationMode === 'bypass' ? 430 : 478} y="304" textAnchor="middle" className="pipeline-small">
          Admitted TPS
        </text>
        <text id="admitted-display" x={appliedInstallationMode === 'bypass' ? 430 : 478} y="321" textAnchor="middle" className="pipeline-value">
          {formatRate(snapshot.admittedTps)}
        </text>

        {appliedInstallationMode === 'installed' && (
          <>
            <rect x="424" y="361" width="12" height="12" rx="1" className="pipeline-valve-neck" />
            <line x1="430" y1="373" x2="430" y2="398" className="pipeline-valve-stem" />
            {renderGate(appliedGateY, 'pipeline-valve-gate', 'applied')}
          </>
        )}
        {appliedInstallationMode === 'installed' &&
          candidateGateY !== null &&
          candidateIndex !== null &&
          candidateIndex !== appliedIndex && (
          <>
            {renderGate(
              candidateGateY,
              `pipeline-valve-ghost pipeline-valve-ghost--${candidateKind ?? 'pending'}`,
              candidateKind ?? 'pending',
            )}
            <text
              x="430"
              y="468"
              textAnchor="middle"
              className={`pipeline-valve-ghost-label pipeline-valve-ghost-label--${candidateKind ?? 'pending'}`}
            >
              {openingPercent(candidateIndex)}% target
            </text>
          </>
        )}
        <path
          d="M401 398 H411 A19 19 0 0 1 449 398 H459 V432 H449 A19 19 0 0 1 411 432 H401 Z M415 415 A15 12.3 0 1 0 445 415 A15 12.3 0 1 0 415 415"
          fillRule="evenodd"
          className="pipeline-valve-body"
          data-visible-bounds="400.25 336.75 59.5 95.25"
        />
        <rect x="401" y="406" width="7" height="18" rx="1" className="pipeline-valve-flange" />
        <rect x="452" y="406" width="7" height="18" rx="1" className="pipeline-valve-flange" />
        <ellipse
          cx="430"
          cy={VALVE_APERTURE.centerY}
          rx={VALVE_APERTURE.radiusX}
          ry={VALVE_APERTURE.radiusY}
          className="pipeline-valve-aperture"
          data-axis-ratio={VALVE_APERTURE.perspectiveRatio.toFixed(2)}
        />

        {appliedInstallationMode === 'installed' && (
          <g
            className={`pipeline-valve-control${adjustable ? '' : ' pipeline-valve-control--disabled'}`}
            role="slider"
            tabIndex={0}
            aria-label="Throttle opening"
            aria-valuemin={0}
            aria-valuemax={11}
            aria-valuenow={rangeIndex}
            aria-valuetext={rangeText}
            aria-disabled={!adjustable}
            aria-readonly={!adjustable}
            data-wheel-phase={wheelPhase}
            data-opening-index={rangeIndex}
            onKeyDown={handleValveKeyDown}
            onBlur={stopHold}
            onClick={(event) => event.stopPropagation()}
          >
            <ellipse cx="430" cy="350" rx="49" ry="29" className="pipeline-valve-focus-ring" />
            {renderWheel(
              'pipeline-valve-wheel',
            )}
            <rect
              x="382"
              y="321"
              width="48"
              height="46"
              className="pipeline-valve-hit-area"
              data-direction="decrease"
              onPointerDown={(event) => handlePointerDown(event, -1)}
              onLostPointerCapture={stopHold}
            />
            <rect
              x="430"
              y="321"
              width="48"
              height="46"
              className="pipeline-valve-hit-area"
              data-direction="increase"
              onPointerDown={(event) => handlePointerDown(event, 1)}
              onLostPointerCapture={stopHold}
            />
          </g>
        )}

        {appliedInstallationMode === 'bypass' && renderDetachedAssembly(
          'pipeline-valve-detached-assembly pipeline-valve-detached-assembly--applied',
        )}

        {installationCandidate === 'bypass' &&
          appliedInstallationMode === 'installed' && (
          <>
            {renderDetachedAssembly(
              `pipeline-valve-installation-ghost pipeline-valve-installation-ghost--${installationCandidateKind}`,
            )}
            <path
              d="M438 383 C470 370 510 354 536 348"
              className={`pipeline-valve-installation-trajectory pipeline-valve-installation-trajectory--${installationCandidateKind}`}
            />
          </>
        )}
        {installationCandidate === 'installed' &&
          appliedInstallationMode === 'bypass' && (
          <>
            <g className={`pipeline-valve-installation-ghost pipeline-valve-installation-ghost--${installationCandidateKind}`}>
              {renderGate(appliedGateY, 'pipeline-valve-gate', 'applied')}
              <rect x="424" y="361" width="12" height="12" rx="1" className="pipeline-valve-neck" />
              <line x1="430" y1="373" x2="430" y2="398" className="pipeline-valve-stem" />
              {renderWheel('pipeline-valve-wheel')}
            </g>
            <path
              d="M542 407 C516 416 474 410 438 383"
              className={`pipeline-valve-installation-trajectory pipeline-valve-installation-trajectory--${installationCandidateKind}`}
            />
          </>
        )}

        <g
          ref={installationControlRef}
          className={`pipeline-valve-installation-control${installationControlAvailable ? '' : ' pipeline-valve-installation-control--disabled'}`}
          role="button"
          tabIndex={0}
          aria-label={appliedInstallationMode === 'bypass'
            ? 'Reinsert throttler valve'
            : 'Remove throttler valve'}
          aria-pressed={appliedInstallationMode === 'bypass'}
          aria-valuetext={installationStatus}
          aria-disabled={!installationControlAvailable}
          data-applied-mode={appliedInstallationMode ?? 'unavailable'}
          data-candidate-mode={installationCandidate ?? ''}
          data-candidate-kind={installationCandidateKind ?? ''}
          data-drag-progress={installationDragProgress.toFixed(2)}
          onPointerDown={handleInstallationPointerDown}
          onPointerMove={handleInstallationPointerMove}
          onPointerUp={handleInstallationPointerUp}
          onPointerCancel={cancelInstallationPreview}
          onKeyDown={handleInstallationKeyDown}
          onClick={(event) => {
            event.stopPropagation()
            handleInstallationClick()
          }}
          onBlur={cancelInstallationPreview}
        >
          {appliedInstallationMode === 'installed' && (
            <rect
              x="416"
              y="334"
              width="28"
              height="32"
              rx="12"
              className="pipeline-valve-installation-hit-area pipeline-valve-installation-hit-area--wheel-grip"
              data-installation-grip="wheel"
            />
          )}
          {appliedInstallationMode === 'bypass' ? (
            <rect
              x={VALVE_INSTALLATION_CONTROL.bypassTarget.x}
              y={VALVE_INSTALLATION_CONTROL.bypassTarget.y}
              width={VALVE_INSTALLATION_CONTROL.bypassTarget.width}
              height={VALVE_INSTALLATION_CONTROL.bypassTarget.height}
              rx="10"
              className="pipeline-valve-installation-hit-area"
            />
          ) : (
            <rect
              x={VALVE_INSTALLATION_CONTROL.installedTarget.x}
              y={VALVE_INSTALLATION_CONTROL.installedTarget.y}
              width={VALVE_INSTALLATION_CONTROL.installedTarget.width}
              height={VALVE_INSTALLATION_CONTROL.installedTarget.height}
              rx="8"
              className="pipeline-valve-installation-hit-area"
            />
          )}
          <rect
            x={appliedInstallationMode === 'bypass'
              ? VALVE_INSTALLATION_CONTROL.bypassTarget.x
              : VALVE_INSTALLATION_CONTROL.installedTarget.x}
            y={appliedInstallationMode === 'bypass'
              ? VALVE_INSTALLATION_CONTROL.bypassTarget.y
              : VALVE_INSTALLATION_CONTROL.installedTarget.y}
            width={appliedInstallationMode === 'bypass'
              ? VALVE_INSTALLATION_CONTROL.bypassTarget.width
              : VALVE_INSTALLATION_CONTROL.installedTarget.width}
            height={appliedInstallationMode === 'bypass'
              ? VALVE_INSTALLATION_CONTROL.bypassTarget.height
              : VALVE_INSTALLATION_CONTROL.installedTarget.height}
            rx="10"
            className="pipeline-valve-installation-focus-ring"
          />
        </g>

        <text x="430" y="454" textAnchor="middle" className="pipeline-valve-opening-label">
          {appliedInstallationMode === 'bypass'
            ? 'BYPASS · APPLIED'
            : `${openingPercent(appliedIndex)}% OPEN`}
        </text>
        <text x="430" y="468" textAnchor="middle" className="pipeline-valve-readonly-label">
          {appliedInstallationMode === 'bypass'
            ? `${openingPercent(appliedIndex)}% SAVED · THROTTLE IGNORED`
            : !adjustable
              ? snapshot.requestedTps.applyMode === 'unavailable'
                ? 'UNAVAILABLE'
                : 'READ ONLY'
              : installationCandidateKind === 'pending'
                ? `${targetInstallationMode.toUpperCase()} PENDING`
                : ''}
        </text>
        {installationError !== null && (
          <text
            x="430"
            y="486"
            textAnchor="middle"
            className="pipeline-valve-installation-error"
            role="status"
          >
            {installationError}
          </text>
        )}
      </g>
    </g>
  )
}
