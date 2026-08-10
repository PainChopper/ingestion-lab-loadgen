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
  ThrottlerSnapshot,
} from '../../model/loadgen'
import { queuePressureColor } from '../../model/queueFlowState'
import { formatRate } from './formatters'
import { ACTOR_GEOMETRY } from './geometry'
import {
  getValveWheelKnobs,
  getValveTargets,
  nextWheelPhase,
  openingPercent,
  VALVE_APERTURE,
  VALVE_FLANGES,
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
}: ThrottlerActorProps) {
  const geometry = ACTOR_GEOMETRY.throttler
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
  const previousAppliedIndex = useRef(appliedIndex)
  const holdTimer = useRef<number | null>(null)
  const repeatTimer = useRef<number | null>(null)
  const releaseListeners = useRef<(() => void) | null>(null)
  const candidateIndexRef = useRef(candidateIndex ?? appliedIndex)
  const wheelPhaseRef = useRef(confirmedWheelPhase)
  const targetsRef = useRef(targets)
  const adjustableRef = useRef(adjustable)

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

  return (
    <>
      <text
        x={geometry.title.x}
        y={geometry.title.y}
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
            x={geometry.bounds.x}
            y={geometry.bounds.y}
            width={geometry.bounds.width}
            height={geometry.bounds.height}
            rx="5"
            className="pipeline-actor-box"
          />
        </g>
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

        <text x="382" y="304" textAnchor="middle" className="pipeline-small">
          Requested TPS
        </text>
        <text id="requested-display" x="382" y="321" textAnchor="middle" className="pipeline-value">
          {formatRate(snapshot.requestedTps.applied)}
        </text>
        <text x="478" y="304" textAnchor="middle" className="pipeline-small">
          Admitted TPS
        </text>
        <text id="admitted-display" x="478" y="321" textAnchor="middle" className="pipeline-value">
          {formatRate(snapshot.admittedTps)}
        </text>

        <rect x="424" y="361" width="12" height="12" rx="1" className="pipeline-valve-neck" />
        <line x1="430" y1="373" x2="430" y2="398" className="pipeline-valve-stem" />
        {renderGate(appliedGateY, 'pipeline-valve-gate', 'applied')}
        {candidateGateY !== null &&
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
          <g className="pipeline-valve-wheel" data-outer-bounds="400.25 336.75 59.5 26.5">
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
          <rect
            x="382"
            y="321"
            width="48"
            height="58"
            className="pipeline-valve-hit-area"
            data-direction="decrease"
            onPointerDown={(event) => handlePointerDown(event, -1)}
            onLostPointerCapture={stopHold}
          />
          <rect
            x="430"
            y="321"
            width="48"
            height="58"
            className="pipeline-valve-hit-area"
            data-direction="increase"
            onPointerDown={(event) => handlePointerDown(event, 1)}
            onLostPointerCapture={stopHold}
          />
        </g>

        <text x="430" y="454" textAnchor="middle" className="pipeline-valve-opening-label">
          {openingPercent(appliedIndex)}% OPEN
        </text>
        {!adjustable && (
          <text x="430" y="468" textAnchor="middle" className="pipeline-valve-readonly-label">
            {snapshot.requestedTps.applyMode === 'unavailable' ? 'UNAVAILABLE' : 'READ ONLY'}
          </text>
        )}
      </g>
    </>
  )
}
