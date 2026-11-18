import { clsx } from 'clsx'
import {
  getValveWheelKnobs,
  VALVE_APERTURE,
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
          cx="430"
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
      <line x1="430" y1="395" x2="430" y2={gateY} />
      <ellipse cx="430" cy={gateY} rx={VALVE_PISTON.radiusX} ry={VALVE_PISTON.radiusY} />
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
      <ellipse cx="430" cy="350" rx="27" ry="10.5" className="pipeline-valve-wheel-rim" />
      <ellipse cx="430" cy="350" rx="20.5" ry="7" className="pipeline-valve-wheel-inner-rim" />
      <g transform="translate(430 350) scale(1 0.42)">
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
  `rotate(${VALVE_DETACHED_ASSEMBLY.rotationDegrees} 430 350)`,
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
}
