import { clsx } from 'clsx'
import { useId, useLayoutEffect, useRef, useState } from 'react'
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
  ThrottlerValveBody,
  ThrottlerValveConnectors,
  ThrottlerValveDetachedAssembly,
  ThrottlerValveGate,
  ThrottlerValveInstallationPreview,
  ThrottlerValveInstalledStem,
} from './ThrottlerValveSvg'
import {
  getValveTargets,
  MAX_OPENING_INDEX,
  nextWheelPhase,
  openingPercent,
  VALVE_APERTURE,
  VALVE_READOUT_Y,
  valvePistonCenterY,
  valueToOpeningIndex,
  valveIsAdjustable,
} from './throttlerValve'
import {
  ThrottlerValveInstallationControl,
  ThrottlerValveOpeningControl,
} from './ThrottlerValveControls'
import { useValveInstallation } from './useValveInstallation'

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
  const openingIndex = candidateIndex ?? appliedIndex
  const openingIndexRef = useRef(openingIndex)
  const wheelPhase = nextWheelPhase(0, openingIndex - initialAppliedIndex)

  // A batched rollback can leave the rendered index unchanged after the ref advances.
  useLayoutEffect(() => {
    openingIndexRef.current = openingIndex
  })

  const requestDelta = (delta: number) => {
    if (!adjustable || targets === null) return false

    const sourceIndex = openingIndexRef.current
    const targetIndex = Math.min(MAX_OPENING_INDEX, Math.max(0, sourceIndex + delta))
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
      Home: -MAX_OPENING_INDEX,
      End: MAX_OPENING_INDEX,
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
  const installation = useValveInstallation(appliedInstalled, installedControl)
  const { installationCandidate, installationCandidateKind, installationError, targetInstalled } =
    installation

  const appliedGateY = valvePistonCenterY(appliedIndex)
  const candidateGateY =
    candidateIndex === null ? null : valvePistonCenterY(candidateIndex ?? appliedIndex)
  const candidateState = candidateKind === null ? 'applied' : candidateKind
  const rangeIndex = openingIndex
  const rangeText = `${openingPercent(rangeIndex)}% open, ${candidateState}; applied ${openingPercent(appliedIndex)}% open`
  const valveStyle = {
    '--pipeline-valve-flow-color': flowColor(upstreamChannel),
  } as CSSProperties

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
        <ThrottlerValveConnectors portraitPipe={portrait ? portraitPipe : null} />

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
            <ThrottlerValveInstalledStem />
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
                x={VALVE_APERTURE.centerX}
                y={VALVE_READOUT_Y.detail}
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
        <ThrottlerValveBody />

        {appliedInstalled === true && (
          <ThrottlerValveOpeningControl
            adjustable={adjustable}
            rangeIndex={rangeIndex}
            rangeText={rangeText}
            wheelPhase={wheelPhase}
            handlePointerDown={handlePointerDown}
            handleValveKeyDown={handleValveKeyDown}
            cancelOpening={cancelOpening}
          />
        )}

        {appliedInstalled === false && (
          <ThrottlerValveDetachedAssembly
            wheelPhase={wheelPhase}
            className="pipeline-valve-detached-assembly pipeline-valve-detached-assembly--applied"
          />
        )}

        <ThrottlerValveInstallationPreview
          installationCandidate={installationCandidate}
          installationCandidateKind={installationCandidateKind}
          appliedInstalled={appliedInstalled}
          appliedGateY={appliedGateY}
          wheelPhase={wheelPhase}
        />

        {installedControl !== undefined && (
          <ThrottlerValveInstallationControl
            appliedInstalled={appliedInstalled}
            installationStatusId={installationStatusId}
            installation={installation}
          />
        )}

        <text
          x={VALVE_APERTURE.centerX}
          y={VALVE_READOUT_Y.opening}
          textAnchor="middle"
          className="pipeline-valve-opening-label"
        >
          {appliedInstalled === false
            ? 'BYPASS · APPLIED'
            : `${openingPercent(appliedIndex)}% OPEN`}
        </text>
        <text
          x={VALVE_APERTURE.centerX}
          y={VALVE_READOUT_Y.detail}
          textAnchor="middle"
          className="pipeline-valve-readonly-label"
        >
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
            x={VALVE_APERTURE.centerX}
            y={VALVE_READOUT_Y.installationError}
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
