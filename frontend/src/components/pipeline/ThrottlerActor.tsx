import { clsx } from 'clsx'
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import type { CSSProperties, KeyboardEvent, PointerEvent as ReactPointerEvent } from 'react'
import type { ChannelSnapshot, SelectableId, ThrottlerSnapshot } from '../../model/loadgen'
import type { DesiredControl } from '../../hooks/useDesiredControl'
import { useHoldRepeat } from '../../hooks/useHoldRepeat'
import { channelPressureColor } from '../../model/channelFlowState'
import { formatRate } from './formatters'
import { ACTOR_GEOMETRY, type PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  ThrottlerValveApertureClip,
  ThrottlerValveDetachedAssembly,
  ThrottlerValveGate,
  ThrottlerValveWheel,
} from './ThrottlerValveSvg'
import {
  getValveTargets,
  nextWheelPhase,
  openingPercent,
  VALVE_APERTURE,
  VALVE_FLANGES,
  VALVE_INSTALLATION_CONTROL,
  VALVE_OPENING_CONTROLS,
  valvePistonCenterY,
  valueToOpeningIndex,
  valveIsAdjustable,
} from './throttlerValve'

interface ThrottlerActorProps {
  snapshot: ThrottlerSnapshot
  upstreamChannel: ChannelSnapshot
  requestedTpsControl: DesiredControl<number>
  installedControl?: DesiredControl<boolean>
  selected: boolean
  onSelect: (id: SelectableId) => void
  geometry: PipelineGeometry['actors']['throttler']
  orientation: PipelineOrientation
}

function flowColor(channel: ChannelSnapshot): string {
  if (channel.flowState === 'connection-error') return 'var(--red)'
  if (channel.flowState === 'stopped') return 'var(--muted)'
  return channelPressureColor(channel.displayedPressure)
}

export function ThrottlerActor({
  snapshot,
  upstreamChannel,
  requestedTpsControl,
  installedControl,
  selected,
  onSelect,
  geometry,
  orientation,
}: ThrottlerActorProps) {
  const localGeometry = ACTOR_GEOMETRY.throttler
  const resolvedGeometry = geometry
  const portrait = orientation === 'portrait'
  const actorTransform =
    resolvedGeometry.transform.x === 0 && resolvedGeometry.transform.y === 0
      ? undefined
      : `translate(${resolvedGeometry.transform.x} ${resolvedGeometry.transform.y})`
  const portraitPipe = 'portraitPipe' in resolvedGeometry ? resolvedGeometry.portraitPipe : null
  const targets = getValveTargets(snapshot.requestedTps)
  const adjustable =
    valveIsAdjustable(snapshot.requestedTps) &&
    requestedTpsControl.available &&
    requestedTpsControl.phase !== 'pending'
  const appliedIndex = valueToOpeningIndex(
    snapshot.requestedTps.applied,
    snapshot.requestedTps,
    targets,
  )
  const candidateValue = requestedTpsControl.phase === 'idle' ? null : requestedTpsControl.desired
  const candidateIndex =
    candidateValue === null
      ? null
      : valueToOpeningIndex(candidateValue, snapshot.requestedTps, targets)
  const candidateKind = requestedTpsControl.phase === 'idle' ? null : requestedTpsControl.phase
  const [initialAppliedIndex] = useState(appliedIndex)
  const [installationDragProgress, setInstallationDragProgress] = useState(0)
  const previousAppliedMode = useRef(snapshot.installed.applied)
  const openingIndex = candidateIndex ?? appliedIndex
  const openingIndexRef = useRef(openingIndex)
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
  const wheelPhase = nextWheelPhase(0, openingIndex - initialAppliedIndex)

  // A batched rollback can leave the rendered index unchanged after the ref advances.
  useLayoutEffect(() => {
    openingIndexRef.current = openingIndex
  })

  const cancelInstallationPreview = useCallback(() => {
    installationDrag.current = null
    installationDragProgressRef.current = 0
    cancelInstalled?.()
    setInstallationDragProgress(0)
  }, [cancelInstalled])

  useEffect(() => {
    if (cancelInstalled === undefined) return
    const handleWindowBlur = () => cancelInstallationPreview()
    window.addEventListener('blur', handleWindowBlur)
    return () => window.removeEventListener('blur', handleWindowBlur)
  }, [cancelInstallationPreview, cancelInstalled])

  useEffect(() => {
    if (previousAppliedMode.current === snapshot.installed.applied) return
    previousAppliedMode.current = snapshot.installed.applied
    cancelInstallationPreview()
    if (installationFocusRequested.current) {
      installationControlRef.current?.focus()
      installationFocusRequested.current = false
    }
  }, [cancelInstallationPreview, snapshot.installed.applied])

  const requestDelta = (delta: number) => {
    if (!adjustable || targets === null) return false

    const sourceIndex = openingIndexRef.current
    const targetIndex = Math.min(11, Math.max(0, sourceIndex + delta))
    if (targetIndex === sourceIndex) return false

    openingIndexRef.current = targetIndex
    requestedTpsControl.preview(targets[targetIndex])
    return true
  }

  const openingHold = useHoldRepeat({
    step: requestDelta,
    onCommit: requestedTpsControl.commit,
    onCancel: requestedTpsControl.cancel,
  })

  const cancelOpening = () => {
    if (!openingHold.cancel()) requestedTpsControl.cancel()
  }

  const handlePointerDown = (event: ReactPointerEvent<SVGRectElement>, delta: -1 | 1) => {
    event.preventDefault()
    event.stopPropagation()
    openingHold.start(event.pointerId, delta)
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
    if (delta === undefined || event.repeat) return
    event.preventDefault()
    event.stopPropagation()
    if (requestDelta(delta)) void requestedTpsControl.commit()
  }

  const installationStatusId = useId()
  const appliedInstalled = snapshot.installed.applied
  const metricGeometry =
    appliedInstalled === false
      ? resolvedGeometry.metrics.bypass
      : resolvedGeometry.metrics.installed
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
      event.preventDefault()
      event.stopPropagation()
      cancelInstallationPreview()
      return
    }
    if ((event.key !== 'Enter' && event.key !== ' ') || event.repeat) return
    event.preventDefault()
    event.stopPropagation()
    requestInstalled()
  }

  const appliedGateY = valvePistonCenterY(appliedIndex)
  const candidateGateY =
    candidateIndex === null ? null : valvePistonCenterY(candidateIndex ?? appliedIndex)
  const candidateState = candidateKind === null ? 'applied' : candidateKind
  const rangeIndex = openingIndex
  const rangeText = `${openingPercent(rangeIndex)}% open, ${candidateState}; applied ${openingPercent(appliedIndex)}% open`
  const valveStyle = {
    '--pipeline-valve-flow-color': flowColor(upstreamChannel),
  } as CSSProperties

  const installationStatus =
    installationCandidate === null
      ? `${appliedInstalled === null ? 'unavailable' : appliedInstalled ? 'installed' : 'bypass'} applied`
      : `${installationCandidate ? 'installed' : 'bypass'} ${installationCandidateKind}; ${appliedInstalled === null ? 'unavailable' : appliedInstalled ? 'installed' : 'bypass'} applied`

  return (
    <g transform={actorTransform} data-pipeline-orientation={orientation}>
      <text
        x={resolvedGeometry.renderTitle.x}
        y={resolvedGeometry.renderTitle.y}
        textAnchor={resolvedGeometry.renderTitle.anchor}
        className="pipeline-title pipeline-title--throttler"
      >
        THROTTLER
      </text>
      <g id="throttler-actor" className="pipeline-actor" style={valveStyle}>
        <ThrottlerValveApertureClip />
        <g
          className={clsx('pipeline-selectable', selected && 'pipeline-selectable--selected')}
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
        {portrait && portraitPipe !== null ? (
          <>
            <path
              d={portraitPipe.input.d}
              className="pipeline-valve-flow-line"
              fill="none"
              data-connector-side="portrait-input"
            />
            <path
              d={portraitPipe.output.d}
              className="pipeline-valve-flow-line"
              fill="none"
              data-connector-side="portrait-output"
            />
            <circle
              cx={portraitPipe.input.start.x}
              cy={portraitPipe.input.start.y}
              r="7"
              className="pipeline-port"
            />
            <circle
              cx={portraitPipe.output.end.x}
              cy={portraitPipe.output.end.y}
              r="7"
              className="pipeline-port"
            />
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

        {appliedInstalled === true && (
          <>
            <text
              x={resolvedGeometry.metrics.installed.requested.caption.x}
              y={resolvedGeometry.metrics.installed.requested.caption.y}
              textAnchor={resolvedGeometry.metrics.installed.requested.caption.anchor}
              className="pipeline-small"
              data-actor-metric="requested-caption"
            >
              Requested TPS
            </text>
            <text
              id="requested-display"
              x={resolvedGeometry.metrics.installed.requested.value.x}
              y={resolvedGeometry.metrics.installed.requested.value.y}
              textAnchor={resolvedGeometry.metrics.installed.requested.value.anchor}
              className="pipeline-value"
              data-actor-metric="requested-value"
            >
              {formatRate(requestedTpsControl.desired)}
            </text>
          </>
        )}
        <text
          x={metricGeometry.admitted.caption.x}
          y={metricGeometry.admitted.caption.y}
          textAnchor={metricGeometry.admitted.caption.anchor}
          className="pipeline-small"
          data-actor-metric="admitted-caption"
        >
          Admitted TPS
        </text>
        <text
          id="admitted-display"
          x={metricGeometry.admitted.value.x}
          y={metricGeometry.admitted.value.y}
          textAnchor={metricGeometry.admitted.value.anchor}
          className="pipeline-value"
          data-actor-metric="admitted-value"
        >
          {formatRate(snapshot.admittedTps)}
        </text>

        {appliedInstalled === true && (
          <>
            <rect x="424" y="361" width="12" height="12" rx="1" className="pipeline-valve-neck" />
            <line x1="430" y1="373" x2="430" y2="398" className="pipeline-valve-stem" />
            <ThrottlerValveGate
              gateY={appliedGateY}
              className="pipeline-valve-gate"
              kind="applied"
            />
          </>
        )}
        {appliedInstalled === true &&
          candidateGateY !== null &&
          candidateIndex !== null &&
          candidateIndex !== appliedIndex && (
            <>
              <ThrottlerValveGate
                gateY={candidateGateY}
                className={clsx(
                  'pipeline-valve-ghost',
                  `pipeline-valve-ghost--${candidateKind ?? 'pending'}`,
                )}
                kind={candidateKind ?? 'pending'}
              />
              <text
                x="430"
                y="468"
                textAnchor="middle"
                className={clsx(
                  'pipeline-valve-ghost-label',
                  `pipeline-valve-ghost-label--${candidateKind ?? 'pending'}`,
                )}
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

        {appliedInstalled === true && (
          <g
            className={clsx(
              'pipeline-valve-control',
              !adjustable && 'pipeline-valve-control--disabled',
            )}
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
            onBlur={cancelOpening}
            onClick={(event) => event.stopPropagation()}
          >
            <ellipse cx="430" cy="350" rx="49" ry="29" className="pipeline-valve-focus-ring" />
            <ThrottlerValveWheel wheelPhase={wheelPhase} className="pipeline-valve-wheel" />
            <rect
              x="400.25"
              y="336.75"
              width="29.75"
              height="26.5"
              className="pipeline-valve-wheel-hit-area"
              data-wheel-direction="decrease"
              aria-hidden="true"
              onPointerDown={(event) => handlePointerDown(event, -1)}
              onLostPointerCapture={cancelOpening}
            />
            <rect
              x="430"
              y="336.75"
              width="29.75"
              height="26.5"
              className="pipeline-valve-wheel-hit-area"
              data-wheel-direction="increase"
              aria-hidden="true"
              onPointerDown={(event) => handlePointerDown(event, 1)}
              onLostPointerCapture={cancelOpening}
            />
            <rect
              x={VALVE_OPENING_CONTROLS.decrease.x}
              y={VALVE_OPENING_CONTROLS.decrease.y}
              width={VALVE_OPENING_CONTROLS.decrease.width}
              height={VALVE_OPENING_CONTROLS.decrease.height}
              className="pipeline-valve-hit-area"
              data-direction="decrease"
              onPointerDown={(event) => handlePointerDown(event, -1)}
              onLostPointerCapture={cancelOpening}
            />
            <rect
              x={VALVE_OPENING_CONTROLS.increase.x}
              y={VALVE_OPENING_CONTROLS.increase.y}
              width={VALVE_OPENING_CONTROLS.increase.width}
              height={VALVE_OPENING_CONTROLS.increase.height}
              className="pipeline-valve-hit-area"
              data-direction="increase"
              onPointerDown={(event) => handlePointerDown(event, 1)}
              onLostPointerCapture={cancelOpening}
            />
          </g>
        )}

        {appliedInstalled === false && (
          <ThrottlerValveDetachedAssembly
            wheelPhase={wheelPhase}
            className="pipeline-valve-detached-assembly pipeline-valve-detached-assembly--applied"
          />
        )}

        {installationCandidate === false && appliedInstalled === true && (
          <>
            <ThrottlerValveDetachedAssembly
              wheelPhase={wheelPhase}
              className={`pipeline-valve-installation-ghost pipeline-valve-installation-ghost--${installationCandidateKind}`}
            />
            <path
              d="M438 383 C470 370 510 354 536 348"
              className={`pipeline-valve-installation-trajectory pipeline-valve-installation-trajectory--${installationCandidateKind}`}
            />
          </>
        )}
        {installationCandidate === true && appliedInstalled === false && (
          <>
            <g
              className={`pipeline-valve-installation-ghost pipeline-valve-installation-ghost--${installationCandidateKind}`}
            >
              <ThrottlerValveGate
                gateY={appliedGateY}
                className="pipeline-valve-gate"
                kind="applied"
              />
              <rect x="424" y="361" width="12" height="12" rx="1" className="pipeline-valve-neck" />
              <line x1="430" y1="373" x2="430" y2="398" className="pipeline-valve-stem" />
              <ThrottlerValveWheel wheelPhase={wheelPhase} className="pipeline-valve-wheel" />
            </g>
            <path
              d="M542 407 C516 416 474 410 438 383"
              className={`pipeline-valve-installation-trajectory pipeline-valve-installation-trajectory--${installationCandidateKind}`}
            />
          </>
        )}

        {installedControl !== undefined && (
          <g
            ref={installationControlRef}
            className={clsx(
              'pipeline-valve-installation-control',
              !installationControlAvailable && 'pipeline-valve-installation-control--disabled',
            )}
            role="button"
            tabIndex={0}
            aria-label={
              appliedInstalled === false ? 'Reinsert throttler valve' : 'Remove throttler valve'
            }
            aria-pressed={appliedInstalled === false}
            aria-describedby={installationStatusId}
            aria-disabled={!installationControlAvailable}
            data-applied-mode={
              appliedInstalled === null ? 'unavailable' : appliedInstalled ? 'installed' : 'bypass'
            }
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
            <desc id={installationStatusId}>{installationStatus}</desc>
            {appliedInstalled === true && (
              <rect
                x={VALVE_INSTALLATION_CONTROL.installedTarget.x}
                y={VALVE_INSTALLATION_CONTROL.installedTarget.y}
                width={VALVE_INSTALLATION_CONTROL.installedTarget.width}
                height={VALVE_INSTALLATION_CONTROL.installedTarget.height}
                rx="8"
                className="pipeline-valve-installation-hit-area pipeline-valve-installation-hit-area--wheel-grip"
                data-installation-grip="wheel"
              />
            )}
            {appliedInstalled === false ? (
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
              x={
                appliedInstalled === false
                  ? VALVE_INSTALLATION_CONTROL.bypassTarget.x
                  : VALVE_INSTALLATION_CONTROL.installedTarget.x
              }
              y={
                appliedInstalled === false
                  ? VALVE_INSTALLATION_CONTROL.bypassTarget.y
                  : VALVE_INSTALLATION_CONTROL.installedTarget.y
              }
              width={
                appliedInstalled === false
                  ? VALVE_INSTALLATION_CONTROL.bypassTarget.width
                  : VALVE_INSTALLATION_CONTROL.installedTarget.width
              }
              height={
                appliedInstalled === false
                  ? VALVE_INSTALLATION_CONTROL.bypassTarget.height
                  : VALVE_INSTALLATION_CONTROL.installedTarget.height
              }
              rx="10"
              className="pipeline-valve-installation-focus-ring"
            />
          </g>
        )}

        <text x="430" y="454" textAnchor="middle" className="pipeline-valve-opening-label">
          {appliedInstalled === false
            ? 'BYPASS · APPLIED'
            : `${openingPercent(appliedIndex)}% OPEN`}
        </text>
        <text x="430" y="468" textAnchor="middle" className="pipeline-valve-readonly-label">
          {appliedInstalled === false
            ? `${openingPercent(appliedIndex)}% SAVED · THROTTLE IGNORED`
            : !adjustable
              ? snapshot.requestedTps.applyMode === 'unavailable'
                ? 'UNAVAILABLE'
                : 'READ ONLY'
              : installationCandidateKind === 'pending'
                ? `${targetInstalled ? 'INSTALLED' : 'BYPASS'} PENDING`
                : ''}
        </text>
        {installationError !== undefined && installationError !== null && (
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
