import type { NumericControlSnapshot } from '../../model/loadgen'

export const OPENING_POSITION_COUNT = 12
export const WHEEL_PHASE_COUNT = 6
export const OPENING_PERCENT_LABELS = Object.freeze([
  0, 9, 18, 27, 36, 45, 55, 64, 73, 82, 91, 100,
])
export const VALVE_WHEEL_CENTER = Object.freeze({ x: 430, y: 350 })
export const VALVE_WHEEL_ORBIT = Object.freeze({ rx: 27, ry: 10.5 })
export const VALVE_APERTURE = Object.freeze({
  centerX: 430,
  centerY: 415,
  radiusX: 15,
  radiusY: 12.3,
  perspectiveRatio: 0.82,
})
export const VALVE_PISTON = Object.freeze({
  radiusX: 12,
  radiusY: 9.84,
  closedCenterY: 415,
  firstPartialCenterY: 407,
  openCenterY: 393,
})
export const VALVE_FLANGES = Object.freeze({ left: 401, right: 459 })
export const VALVE_MARKER_RADIUS = 4

export interface ValveWheelKnob {
  readonly angle: number
  readonly x: number
  readonly y: number
  readonly depth: number
  readonly layer: 'back' | 'front'
}

type ValveCapability = Pick<
  NumericControlSnapshot,
  'min' | 'max' | 'step'
>

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function stabilize(value: number): number {
  return Number(value.toPrecision(12))
}

export function valvePistonCenterY(openingIndex: number): number {
  const boundedIndex = clamp(Math.round(openingIndex), 0, 11)
  if (boundedIndex === 0) return VALVE_PISTON.closedCenterY
  if (boundedIndex === 11) return VALVE_PISTON.openCenterY
  return stabilize(
    VALVE_PISTON.firstPartialCenterY -
      (boundedIndex - 1) *
        (VALVE_PISTON.firstPartialCenterY - 395) / 9,
  )
}

export function valvePassageCenterY(openingIndex: number): number {
  const pistonBottom = valvePistonCenterY(openingIndex) + VALVE_PISTON.radiusY
  return stabilize(Math.max(
    VALVE_APERTURE.centerY,
    pistonBottom + VALVE_MARKER_RADIUS + 1,
  ))
}

function capabilityIsFinite(capability: ValveCapability): boolean {
  return Number.isFinite(capability.min) &&
    Number.isFinite(capability.max) &&
    Number.isFinite(capability.step) &&
    capability.max > capability.min &&
    capability.step > 0
}

export function getValveTargets(
  capability: ValveCapability,
): readonly number[] | null {
  if (!capabilityIsFinite(capability)) return null

  const span = capability.max - capability.min
  const targets = Array.from({ length: OPENING_POSITION_COUNT }, (_, index) => {
    const raw = capability.min + span * index / (OPENING_POSITION_COUNT - 1)
    const stepsFromMin = Math.round((raw - capability.min) / capability.step)
    return stabilize(clamp(
      capability.min + stepsFromMin * capability.step,
      capability.min,
      capability.max,
    ))
  })

  return new Set(targets).size === OPENING_POSITION_COUNT
    ? Object.freeze(targets)
    : null
}

export function valueToOpeningIndex(
  value: number | null,
  capability: ValveCapability,
  targets = getValveTargets(capability),
): number {
  if (value === null || !Number.isFinite(value)) return 0

  if (targets !== null) {
    let nearestIndex = 0
    let nearestDistance = Number.POSITIVE_INFINITY
    targets.forEach((target, index) => {
      const distance = Math.abs(value - target)
      if (distance < nearestDistance) {
        nearestIndex = index
        nearestDistance = distance
      }
    })
    return nearestIndex
  }

  if (!Number.isFinite(capability.min) ||
    !Number.isFinite(capability.max) ||
    capability.max <= capability.min) {
    return 0
  }
  const normalized = clamp(
    (value - capability.min) / (capability.max - capability.min),
    0,
    1,
  )
  return Math.round(normalized * (OPENING_POSITION_COUNT - 1))
}

export function openingPercent(index: number): number {
  return OPENING_PERCENT_LABELS[clamp(
    Math.round(index),
    0,
    OPENING_POSITION_COUNT - 1,
  )]
}

export function nextWheelPhase(phase: number, delta: number): number {
  const next = (phase + delta) % WHEEL_PHASE_COUNT
  return next < 0 ? next + WHEEL_PHASE_COUNT : next
}

export function getValveWheelKnobs(
  phase: number,
): readonly ValveWheelKnob[] {
  const normalizedPhase = nextWheelPhase(phase, 0)
  const orbitOffset = normalizedPhase % 2 === 0 ? 0 : 30
  return Object.freeze(Array.from({ length: WHEEL_PHASE_COUNT }, (_, index) => {
    const angle = orbitOffset + index * 60
    const radians = angle * Math.PI / 180
    const depth = stabilize(Math.sin(radians))
    return Object.freeze({
      angle,
      x: stabilize(VALVE_WHEEL_CENTER.x + Math.cos(radians) * VALVE_WHEEL_ORBIT.rx),
      y: stabilize(VALVE_WHEEL_CENTER.y + depth * VALVE_WHEEL_ORBIT.ry),
      depth,
      layer: depth < 0 ? 'back' as const : 'front' as const,
    })
  }).sort((left, right) =>
    left.depth - right.depth || left.angle - right.angle
  ))
}

export function valveIsAdjustable(control: NumericControlSnapshot): boolean {
  return control.applyMode !== 'unavailable' &&
    control.applied !== null &&
    Number.isFinite(control.applied) &&
    getValveTargets(control) !== null
}
