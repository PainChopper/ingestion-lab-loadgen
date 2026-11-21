import { clsx } from 'clsx'
import type { KeyboardEvent, PointerEvent as ReactPointerEvent } from 'react'
import { ThrottlerValveWheel } from './ThrottlerValveSvg'
import {
  VALVE_FOCUS_RING,
  VALVE_INSTALLATION_CONTROL,
  VALVE_OPENING_CONTROLS,
  VALVE_WHEEL_CENTER,
  VALVE_WHEEL_HIT_AREAS,
} from './throttlerValve'
import type { useValveInstallation } from './useValveInstallation'

interface OpeningControlProps {
  adjustable: boolean
  rangeIndex: number
  maxRangeIndex: number
  rangeText: string
  wheelPhase: number
  handlePointerDown: (event: ReactPointerEvent<SVGRectElement>, delta: -1 | 1) => void
  handleValveKeyDown: (event: KeyboardEvent<SVGGElement>) => void
  cancelOpening: () => void
}

export function ThrottlerValveOpeningControl({
  adjustable,
  rangeIndex,
  maxRangeIndex,
  rangeText,
  wheelPhase,
  handlePointerDown,
  handleValveKeyDown,
  cancelOpening,
}: OpeningControlProps) {
  return (
    <g
      className={clsx('pipeline-valve-control', !adjustable && 'pipeline-valve-control--disabled')}
      role="slider"
      tabIndex={0}
      aria-label="Throttle opening"
      aria-valuemin={0}
      aria-valuemax={maxRangeIndex}
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
      <ellipse
        cx={VALVE_WHEEL_CENTER.x}
        cy={VALVE_WHEEL_CENTER.y}
        rx={VALVE_FOCUS_RING.rx}
        ry={VALVE_FOCUS_RING.ry}
        className="pipeline-valve-focus-ring"
      />
      <ThrottlerValveWheel wheelPhase={wheelPhase} className="pipeline-valve-wheel" />
      <rect
        x={VALVE_WHEEL_HIT_AREAS.decreaseX}
        y={VALVE_WHEEL_HIT_AREAS.y}
        width={VALVE_WHEEL_HIT_AREAS.width}
        height={VALVE_WHEEL_HIT_AREAS.height}
        className="pipeline-valve-wheel-hit-area"
        data-wheel-direction="decrease"
        aria-hidden="true"
        onPointerDown={(event) => handlePointerDown(event, -1)}
        onLostPointerCapture={cancelOpening}
      />
      <rect
        x={VALVE_WHEEL_CENTER.x}
        y={VALVE_WHEEL_HIT_AREAS.y}
        width={VALVE_WHEEL_HIT_AREAS.width}
        height={VALVE_WHEEL_HIT_AREAS.height}
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
  )
}

export function ThrottlerValveInstallationControl({
  appliedInstalled,
  installationStatusId,
  installation,
}: {
  appliedInstalled: boolean | null
  installationStatusId: string
  installation: ReturnType<typeof useValveInstallation>
}) {
  const {
    installationCandidate,
    installationCandidateKind,
    installationControlAvailable,
    installationControlRef,
    installationDragProgress,
    cancelInstallationPreview,
    handleInstallationPointerDown,
    handleInstallationPointerMove,
    handleInstallationPointerUp,
    handleInstallationClick,
    handleInstallationKeyDown,
  } = installation
  const installationStatus =
    installationCandidate === null
      ? `${appliedInstalled === null ? 'unavailable' : appliedInstalled ? 'installed' : 'bypass'} applied`
      : `${installationCandidate ? 'installed' : 'bypass'} ${installationCandidateKind}; ${appliedInstalled === null ? 'unavailable' : appliedInstalled ? 'installed' : 'bypass'} applied`
  return (
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
  )
}
