import { clsx } from 'clsx'
import type { DesiredControlPhase } from '../../hooks/useDesiredControl'
import { ACTOR_GEOMETRY, FLOW_BASELINE, type PipelineGeometry } from './geometry'
import {
  getValveWheelKnobs,
  VALVE_APERTURE,
  VALVE_BODY,
  VALVE_FLANGES,
  VALVE_INSTALLATION_TRAJECTORY,
  VALVE_WHEEL_CENTER,
  VALVE_WHEEL_ORBIT,
  VALVE_DETACHED_ASSEMBLY,
  VALVE_PISTON,
  type ValveWheelKnob,
} from './throttlerValve'

const apertureClipId = 'throttler-valve-aperture-clip'

export function ThrottlerValveApertureClip() {
  return (
    <defs>
      <clipPath id={apertureClipId}>
        <ellipse
          cx={VALVE_APERTURE.centerX}
          cy={VALVE_APERTURE.centerY}
          rx={VALVE_APERTURE.radiusX}
          ry={VALVE_APERTURE.radiusY}
        />
      </clipPath>
    </defs>
  )
}

export function ThrottlerValveGate({
  gateY,
  className,
  kind,
}: {
  gateY: number
  className: string
  kind: 'applied' | 'preview' | 'pending'
}) {
  return (
    <g
      className={className}
      clipPath={`url(#${apertureClipId})`}
      data-gate-kind={kind}
      data-gate-y={gateY.toFixed(3)}
      data-piston-axis-ratio={(VALVE_PISTON.radiusY / VALVE_PISTON.radiusX).toFixed(2)}
    >
      <line x1={VALVE_WHEEL_CENTER.x} y1="395" x2={VALVE_WHEEL_CENTER.x} y2={gateY} />
      <ellipse
        cx={VALVE_WHEEL_CENTER.x}
        cy={gateY}
        rx={VALVE_PISTON.radiusX}
        ry={VALVE_PISTON.radiusY}
      />
    </g>
  )
}

function renderKnob(knob: ValveWheelKnob, index: number) {
  return (
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
}

export function ThrottlerValveWheel({
  wheelPhase,
  className,
  transform,
}: {
  wheelPhase: number
  className: string
  transform?: string
}) {
  const wheelKnobs = getValveWheelKnobs(wheelPhase)
  const backKnobs = wheelKnobs.filter((knob) => knob.layer === 'back')
  const frontKnobs = wheelKnobs.filter((knob) => knob.layer === 'front')

  return (
    <g className={className} transform={transform} data-wheel-phase={wheelPhase}>
      <g className="pipeline-valve-wheel-knobs pipeline-valve-wheel-knobs--back">
        {backKnobs.map(renderKnob)}
      </g>
      <ellipse
        cx={VALVE_WHEEL_CENTER.x}
        cy={VALVE_WHEEL_CENTER.y}
        rx={VALVE_WHEEL_ORBIT.rx}
        ry={VALVE_WHEEL_ORBIT.ry}
        className="pipeline-valve-wheel-rim"
      />
      <ellipse
        cx={VALVE_WHEEL_CENTER.x}
        cy={VALVE_WHEEL_CENTER.y}
        rx="20.5"
        ry="7"
        className="pipeline-valve-wheel-inner-rim"
      />
      <g transform={`translate(${VALVE_WHEEL_CENTER.x} ${VALVE_WHEEL_CENTER.y}) scale(1 0.42)`}>
        <g
          className="pipeline-valve-wheel-inner-motion"
          style={{ transform: `rotate(${wheelPhase * 60}deg)` }}
          data-internal-phase={wheelPhase}
        >
          {Array.from({ length: 5 }, (_, index) => {
            const angle = (index * 72 * Math.PI) / 180
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
}

const detachedTransform = [
  `translate(${VALVE_DETACHED_ASSEMBLY.translateX} ${VALVE_DETACHED_ASSEMBLY.translateY})`,
  `rotate(${VALVE_DETACHED_ASSEMBLY.rotationDegrees} ${VALVE_WHEEL_CENTER.x} ${VALVE_WHEEL_CENTER.y})`,
].join(' ')

export function ThrottlerValveDetachedAssembly({
  wheelPhase,
  className,
}: {
  wheelPhase: number
  className: string
}) {
  return (
    <g
      className={className}
      transform={detachedTransform}
      data-detached-anchor={`${VALVE_DETACHED_ASSEMBLY.wheelCenterX} ${VALVE_DETACHED_ASSEMBLY.wheelCenterY}`}
    >
      <g
        className={
          clsx(className.includes('--applied') && 'pipeline-valve-detached-sway') || undefined
        }
      >
        <ThrottlerValveWheel wheelPhase={wheelPhase} className="pipeline-valve-wheel" />
        <rect x="424" y="363" width="12" height="12" rx="1" className="pipeline-valve-neck" />
        <line
          x1={VALVE_WHEEL_CENTER.x}
          y1="375"
          x2={VALVE_WHEEL_CENTER.x}
          y2="405"
          className="pipeline-valve-stem"
        />
        <ellipse
          cx={VALVE_WHEEL_CENTER.x}
          cy="409"
          rx={VALVE_PISTON.radiusX}
          ry={VALVE_PISTON.radiusY}
          className="pipeline-valve-detached-piston"
        />
      </g>
    </g>
  )
}

type ThrottlerGeometry = PipelineGeometry['actors']['throttler']
type PortraitPipe = Extract<ThrottlerGeometry, { portraitPipe: unknown }>['portraitPipe']

export function ThrottlerValveConnectors({ portraitPipe }: { portraitPipe: PortraitPipe | null }) {
  const { input, output } = ACTOR_GEOMETRY.throttler.ports
  if (portraitPipe !== null) {
    return (
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
    )
  }
  return (
    <>
      <line
        x1={input.x}
        y1={FLOW_BASELINE}
        x2={VALVE_FLANGES.left}
        y2={FLOW_BASELINE}
        className="pipeline-valve-flow-line"
        data-connector-side="left"
      />
      <line
        x1={VALVE_FLANGES.right}
        y1={FLOW_BASELINE}
        x2={output.x}
        y2={FLOW_BASELINE}
        className="pipeline-valve-flow-line"
        data-connector-side="right"
      />
      <circle cx={input.x} cy={FLOW_BASELINE} r="7" className="pipeline-port" />
      <circle cx={output.x} cy={FLOW_BASELINE} r="7" className="pipeline-port" />
    </>
  )
}

export function ThrottlerValveBody() {
  return (
    <>
      <path
        d={VALVE_BODY.d}
        fillRule="evenodd"
        className="pipeline-valve-body"
        data-visible-bounds={VALVE_BODY.visibleBounds}
      />
      <rect
        x={VALVE_FLANGES.left}
        y={VALVE_FLANGES.y}
        width={VALVE_FLANGES.width}
        height={VALVE_FLANGES.height}
        rx="1"
        className="pipeline-valve-flange"
      />
      <rect
        x={VALVE_FLANGES.right - VALVE_FLANGES.width}
        y={VALVE_FLANGES.y}
        width={VALVE_FLANGES.width}
        height={VALVE_FLANGES.height}
        rx="1"
        className="pipeline-valve-flange"
      />
      <ellipse
        cx={VALVE_APERTURE.centerX}
        cy={VALVE_APERTURE.centerY}
        rx={VALVE_APERTURE.radiusX}
        ry={VALVE_APERTURE.radiusY}
        className="pipeline-valve-aperture"
        data-axis-ratio={VALVE_APERTURE.perspectiveRatio.toFixed(2)}
      />
    </>
  )
}

export function ThrottlerValveInstalledStem() {
  return (
    <>
      <rect
        x={VALVE_BODY.neckX}
        y={VALVE_BODY.neckY}
        width="12"
        height="12"
        rx="1"
        className="pipeline-valve-neck"
      />
      <line
        x1={VALVE_WHEEL_CENTER.x}
        y1={VALVE_BODY.stemTopY}
        x2={VALVE_WHEEL_CENTER.x}
        y2={VALVE_BODY.stemBottomY}
        className="pipeline-valve-stem"
      />
    </>
  )
}

export function ThrottlerValveInstallationPreview({
  installationCandidate,
  installationCandidateKind,
  appliedInstalled,
  appliedGateY,
  wheelPhase,
}: {
  installationCandidate: boolean | null
  installationCandidateKind: Exclude<DesiredControlPhase, 'idle'> | null
  appliedInstalled: boolean | null
  appliedGateY: number
  wheelPhase: number
}) {
  return (
    <>
      {installationCandidate === false && appliedInstalled === true && (
        <>
          <ThrottlerValveDetachedAssembly
            wheelPhase={wheelPhase}
            className={`pipeline-valve-installation-ghost pipeline-valve-installation-ghost--${installationCandidateKind}`}
          />
          <path
            d={VALVE_INSTALLATION_TRAJECTORY.removal}
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
            <ThrottlerValveInstalledStem />
            <ThrottlerValveWheel wheelPhase={wheelPhase} className="pipeline-valve-wheel" />
          </g>
          <path
            d={VALVE_INSTALLATION_TRAJECTORY.reinsertion}
            className={`pipeline-valve-installation-trajectory pipeline-valve-installation-trajectory--${installationCandidateKind}`}
          />
        </>
      )}
    </>
  )
}
