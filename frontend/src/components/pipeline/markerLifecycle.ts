import type { QueueId, RunState } from '../../model/loadgen'
import {
  getQueueMarkerCount,
  QUEUE_CABLE_MAX_MARKERS,
} from './queueCableGeometry'

export const MAX_QUEUE_DEPTH_FAMILIES = QUEUE_CABLE_MAX_MARKERS
export const MIN_FLOW_MARKERS = 3
export const MAX_FLOW_MARKERS = 12
export const MAX_HTTP_ATTEMPT_MARKERS = 3
export const MAX_PIPELINE_MARKERS = MAX_QUEUE_DEPTH_FAMILIES * 2 + MAX_FLOW_MARKERS
export const MAX_PENDING_OUTCOMES = MAX_PIPELINE_MARKERS
export const MAX_VISIBLE_WAITING_FAMILIES = 8

const MARKER_SPEED = 70
const FLOW_MARKER_SCALE_TPS = 250_000
const OUTCOME_PULSE_MS = 520
const RETIREMENT_GRACE_MS = 1_000
const REDUCED_MOTION_STEP_MS = 240
export const PRE_VALVE_STOP_PHASE = 0.34
const PRE_VALVE_FAMILY_SPACING = 0.05
const VALVE_ADMISSION_MAX_INTERVAL_MS = 900
const VALVE_ADMISSION_MIN_INTERVAL_MS = 120
const VALVE_OPENING_MAX_INDEX = 11

export type MarkerSlotState = 'inactive' | 'active' | 'retiring'
export type HttpOutcome = 'success' | 'error'
export type MarkerStage =
  | 'reader'
  | 'queue1'
  | 'throttler'
  | 'queue2'
  | 'sender'
  | 'http'
  | 'target'

export interface PipelineMarkerSlotSnapshot {
  readonly slotId: string
  readonly familyId: string | null
  readonly state: MarkerSlotState
  readonly stage: MarkerStage
  readonly phase: number
  readonly queued: boolean
  readonly outcome: HttpOutcome | null
  readonly outcomeVisible: boolean
  readonly pulseProgress: number
}

export interface MarkerLifecycleSnapshot {
  readonly revision: number
  readonly reducedMotion: boolean
  readonly motionElapsedMs: number
  readonly markers: readonly PipelineMarkerSlotSnapshot[]
}

export interface QueueMarkerTelemetry {
  readonly id: QueueId
  readonly depthBatches: number | null
  readonly appliedCapacity: number
  readonly throughputTps: number | null
  readonly dequeueActive: boolean
  readonly blocked: boolean
  readonly enqueuedBatchesTotal: number
  readonly dequeuedBatchesTotal: number
}

export interface HttpMarkerTelemetry {
  readonly inFlightRequests: number
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly connectionError: boolean
}

export interface MarkerLifecycleTelemetry {
  readonly runState: RunState
  readonly reducedMotion: boolean
  readonly valveOpeningIndex: number
  readonly valvePreAdmissionStopPhase: number
  readonly valveExitPhase: number
  readonly queue1: QueueMarkerTelemetry
  readonly queue2: QueueMarkerTelemetry
  readonly http: HttpMarkerTelemetry
  readonly stageTravelLengths: Readonly<Record<MarkerStage, number>>
}

export interface MarkerMicroJitter {
  readonly x: number
  readonly y: number
}

interface MutableMarkerSlot {
  readonly slotId: string
  familyId: string | null
  state: MarkerSlotState
  stage: MarkerStage
  phase: number
  retirementRemainingMs: number | null
  outcome: HttpOutcome | null
  pulseProgress: number
}

type Listener = () => void

export const MARKER_STAGE_ORDER: readonly MarkerStage[] = [
  'reader',
  'queue1',
  'throttler',
  'queue2',
  'sender',
  'http',
  'target',
]

const FLOW_TRAVEL_STAGES = MARKER_STAGE_ORDER.slice(0, -1)

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function positiveInteger(value: number): number {
  return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0
}

function stableHash(value: string): number {
  let hash = 2166136261
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return hash >>> 0
}

export function markerMicroJitter(
  familyId: string,
  slotId: string,
): MarkerMicroJitter {
  const unit = (suffix: string) =>
    stableHash(`${familyId}:${slotId}:${suffix}`) / 0xffffffff
  return Object.freeze({
    x: Number(((unit('x') * 2 - 1) * 1.8).toFixed(3)),
    y: Number(((unit('y') * 2 - 1) * 1.2).toFixed(3)),
  })
}

export function markerWaitingOffset(
  familyId: string,
  slotId: string,
  elapsedMs: number,
  reducedMotion: boolean,
): MarkerMicroJitter {
  if (reducedMotion) return Object.freeze({ x: 0, y: 0 })
  const anchor = markerMicroJitter(familyId, slotId)
  const phase = stableHash(`${familyId}:${slotId}:phase`) / 0xffffffff * Math.PI * 2
  const speed = 0.0016 +
    stableHash(`${familyId}:${slotId}:speed`) / 0xffffffff * 0.0008
  return Object.freeze({
    x: Number((anchor.x + Math.sin(phase + elapsedMs * speed) * 0.65).toFixed(3)),
    y: Number((anchor.y + Math.cos(phase + elapsedMs * speed * 0.83) * 0.45).toFixed(3)),
  })
}

export function valveAdmissionIntervalMs(openingIndex: number): number {
  const boundedIndex = clamp(
    Math.round(openingIndex),
    0,
    VALVE_OPENING_MAX_INDEX,
  )
  if (boundedIndex === 0) return Number.POSITIVE_INFINITY
  if (boundedIndex === VALVE_OPENING_MAX_INDEX) return 0
  const progress = (boundedIndex - 1) / (VALVE_OPENING_MAX_INDEX - 2)
  return VALVE_ADMISSION_MAX_INTERVAL_MS -
    progress * (
      VALVE_ADMISSION_MAX_INTERVAL_MS - VALVE_ADMISSION_MIN_INTERVAL_MS
    )
}

export function getQueueDepthFamilyTarget(
  depthBatches: number | null,
  appliedCapacity: number,
): number {
  return getQueueMarkerCount(depthBatches, appliedCapacity)
}

export function getFlowMarkerTarget(throughputTps: number | null): number {
  if (throughputTps === null || !Number.isFinite(throughputTps)) return 0
  if (throughputTps <= 0) return 0

  return clamp(
    Math.ceil((throughputTps / FLOW_MARKER_SCALE_TPS) * MAX_FLOW_MARKERS),
    MIN_FLOW_MARKERS,
    MAX_FLOW_MARKERS,
  )
}

function evenPhase(index: number, count: number): number {
  if (count <= 0) return 0.5
  return (index + 0.5) / count
}

function nextStage(stage: MarkerStage): MarkerStage | null {
  const index = MARKER_STAGE_ORDER.indexOf(stage)
  return index < 0 || index === MARKER_STAGE_ORDER.length - 1
    ? null
    : MARKER_STAGE_ORDER[index + 1]
}

function lifecycleRank(slot: MutableMarkerSlot): number {
  return MARKER_STAGE_ORDER.indexOf(slot.stage) + slot.phase
}

function createSlots(): MutableMarkerSlot[] {
  return Array.from({ length: MAX_PIPELINE_MARKERS }, (_, ordinal) => ({
    slotId: `pipeline-marker-${ordinal + 1}`,
    familyId: null,
    state: 'inactive',
    stage: 'reader',
    phase: 0,
    retirementRemainingMs: null,
    outcome: null,
    pulseProgress: 0,
  }))
}

export class MarkerLifecycleController {
  private readonly listeners = new Set<Listener>()
  private readonly slots = createSlots()
  private readonly pendingOutcomes: HttpOutcome[] = []
  private revision = 0
  private familySequence = 0
  private runState: RunState = 'idle'
  private reducedMotion = false
  private initialized = false
  private familyTarget = 0
  private throughputCadenceActive = false
  private queue1DequeueActive = false
  private queue2DequeueActive = false
  private valveOpeningIndex = 0
  private valvePreAdmissionStopPhase = PRE_VALVE_STOP_PHASE
  private valveAdmissionElapsedMs = 0
  private motionElapsedMs = 0
  private reducedMotionElapsedMs = 0
  private connectionError = false
  private queue1DepthTarget = 0
  private queue2DepthTarget = 0
  private stageTravelLengths: Readonly<Record<MarkerStage, number>>
  private previousQueue1EnqueuedTotal = 0
  private previousQueue1DequeuedTotal = 0
  private previousQueue2EnqueuedTotal = 0
  private previousQueue2DequeuedTotal = 0
  private previousRequestsStartedTotal = 0
  private previousRequestsCompletedTotal = 0
  private previousRequestsSucceededTotal = 0
  private previousRequestsFailedTotal = 0
  private snapshot: MarkerLifecycleSnapshot

  constructor(telemetry: MarkerLifecycleTelemetry) {
    this.stageTravelLengths = telemetry.stageTravelLengths
    this.snapshot = Object.freeze({
      revision: 0,
      reducedMotion: telemetry.reducedMotion,
      motionElapsedMs: 0,
      markers: Object.freeze([]),
    })
    this.reconcile(telemetry)
  }

  readonly subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  readonly getSnapshot = (): MarkerLifecycleSnapshot => this.snapshot

  reconcile(telemetry: MarkerLifecycleTelemetry): void {
    const totalsReset = this.initialized && (
      telemetry.queue1.enqueuedBatchesTotal < this.previousQueue1EnqueuedTotal ||
      telemetry.queue1.dequeuedBatchesTotal < this.previousQueue1DequeuedTotal ||
      telemetry.queue2.enqueuedBatchesTotal < this.previousQueue2EnqueuedTotal ||
      telemetry.queue2.dequeuedBatchesTotal < this.previousQueue2DequeuedTotal ||
      telemetry.http.requestsStartedTotal < this.previousRequestsStartedTotal ||
      telemetry.http.requestsCompletedTotal < this.previousRequestsCompletedTotal ||
      telemetry.http.requestsSucceededTotal < this.previousRequestsSucceededTotal ||
      telemetry.http.requestsFailedTotal < this.previousRequestsFailedTotal
    )
    if (totalsReset) this.reset()

    this.runState = telemetry.runState
    this.reducedMotion = telemetry.reducedMotion
    this.queue1DequeueActive = (telemetry.queue1.throughputTps ?? 0) > 0
    this.queue2DequeueActive = (telemetry.queue2.throughputTps ?? 0) > 0
    const nextValveOpeningIndex = clamp(
      Math.round(telemetry.valveOpeningIndex),
      0,
      VALVE_OPENING_MAX_INDEX,
    )
    const nextStopPhase = clamp(telemetry.valvePreAdmissionStopPhase, 0, 1)
    const nextExitPhase = clamp(telemetry.valveExitPhase, nextStopPhase, 1)
    this.remapValvePositions(
      nextValveOpeningIndex,
      nextStopPhase,
      nextExitPhase,
    )
    if (nextValveOpeningIndex !== this.valveOpeningIndex) {
      this.valveAdmissionElapsedMs = 0
    }
    this.valveOpeningIndex = nextValveOpeningIndex
    this.valvePreAdmissionStopPhase = nextStopPhase
    this.connectionError = telemetry.http.connectionError
    this.queue1DepthTarget = getQueueDepthFamilyTarget(
      telemetry.queue1.depthBatches,
      telemetry.queue1.appliedCapacity,
    )
    this.queue2DepthTarget = getQueueDepthFamilyTarget(
      telemetry.queue2.depthBatches,
      telemetry.queue2.appliedCapacity,
    )
    this.throughputCadenceActive = this.maximumThroughput(telemetry) > 0
    this.stageTravelLengths = telemetry.stageTravelLengths

    if (!this.initialized) {
      this.initialized = true
      this.reconcilePool(telemetry)
    } else {
      this.captureOutcomes(telemetry)
      if (this.runState !== 'paused') {
        this.reconcilePool(telemetry)
      }
    }

    if (this.runState !== 'paused') {
      if (this.reducedMotion) this.exposeReducedMotionArrivals()
      this.assignPendingOutcomes()
      if (telemetry.http.connectionError) this.assignConnectionErrors()
    }
    this.captureTotals(telemetry)
    this.publish()
  }

  advance(deltaMs: number): void {
    if (
      this.runState === 'paused' ||
      !Number.isFinite(deltaMs) ||
      deltaMs <= 0
    ) {
      return
    }

    let changed = false
    if (this.runState === 'running' && !this.reducedMotion) {
      this.motionElapsedMs += deltaMs
      changed = true
    }
    const valveReleaseSlots = this.valveReleaseSlots(deltaMs)
    let reducedMotionDeltaMs = deltaMs
    if (this.reducedMotion) {
      this.reducedMotionElapsedMs += deltaMs
      const steps = Math.floor(
        this.reducedMotionElapsedMs / REDUCED_MOTION_STEP_MS,
      )
      reducedMotionDeltaMs = steps * REDUCED_MOTION_STEP_MS
      this.reducedMotionElapsedMs -= reducedMotionDeltaMs
    }
    const orderedSlots = this.slots.slice().sort(
      (left, right) => lifecycleRank(right) - lifecycleRank(left),
    )
    for (const slot of orderedSlots) {
      if (slot.state === 'inactive') continue

      if (slot.state === 'retiring') {
        const remaining = slot.retirementRemainingMs ?? RETIREMENT_GRACE_MS
        slot.retirementRemainingMs = Math.max(0, remaining - deltaMs)
        changed = true
        if (slot.retirementRemainingMs === 0) {
          this.deactivateSlot(slot)
          continue
        }
      }

      if (
        slot.stage === 'target' &&
        slot.phase >= 1 &&
        slot.outcome !== null &&
        (this.runState === 'running' || this.reducedMotion)
      ) {
        changed = this.advanceOutcomeDwell(slot, deltaMs) || changed
        continue
      }
      if (this.runState !== 'running') continue
      if (this.reducedMotion) {
        const downstream = slot.stage === 'throttler'
          ? slot.phase > this.valvePreAdmissionStopPhase ||
            valveReleaseSlots.has(slot.slotId)
          : MARKER_STAGE_ORDER.indexOf(slot.stage) >
            MARKER_STAGE_ORDER.indexOf('throttler')
        if (!downstream || reducedMotionDeltaMs === 0) continue
      }

      changed = this.advanceFamily(
        slot,
        this.reducedMotion ? reducedMotionDeltaMs : deltaMs,
        valveReleaseSlots.has(slot.slotId),
      ) || changed
    }

    if (this.reducedMotion) {
      changed = this.exposeReducedMotionArrivals() || changed
    }
    changed = this.assignPendingOutcomes() || changed
    if (this.connectionError) {
      changed = this.assignConnectionErrors() || changed
    }
    if (changed) this.publish()
  }

  private maximumThroughput(telemetry: MarkerLifecycleTelemetry): number {
    return Math.max(
      0,
      telemetry.queue1.throughputTps ?? 0,
      telemetry.queue2.throughputTps ?? 0,
    )
  }

  private desiredThroughputTarget(telemetry: MarkerLifecycleTelemetry): number {
    if (telemetry.runState === 'idle') return 0
    const throughputTps = this.maximumThroughput(telemetry)
    if (throughputTps <= 0) return 0

    return Math.max(
      getFlowMarkerTarget(throughputTps),
      clamp(
        positiveInteger(telemetry.http.inFlightRequests),
        0,
        MAX_HTTP_ATTEMPT_MARKERS,
      ),
    )
  }

  private reconcilePool(telemetry: MarkerLifecycleTelemetry): void {
    const requestedFamilyTarget = Math.min(
      MAX_PIPELINE_MARKERS,
      this.queue1DepthTarget +
        this.queue2DepthTarget +
        this.desiredThroughputTarget(telemetry),
    )
    const heldByZeroThroughput =
      telemetry.runState === 'running' &&
      this.familySlots().length > 0 &&
      (!this.queue1DequeueActive || !this.queue2DequeueActive)
    this.familyTarget = heldByZeroThroughput
      ? this.activeFamilySlots().length
      : requestedFamilyTarget
    this.reconcileFamilySlots(this.familyTarget)
  }

  private reconcileFamilySlots(target: number): void {
    let active = this.activeFamilySlots()
    if (active.length > target) {
      const ordered = active
        .slice()
        .sort((left, right) => lifecycleRank(left) - lifecycleRank(right))
      const survivorIndexes = new Set(
        Array.from({ length: target }, (_, index) =>
          Math.min(
            ordered.length - 1,
            Math.floor(((index + 0.5) * ordered.length) / target),
          )
        ),
      )
      ordered.forEach((slot, index) => {
        if (!survivorIndexes.has(index)) this.retireSlot(slot)
      })
      active = this.activeFamilySlots()
    }

    const retiring = this.slots
      .filter((slot) => slot.state === 'retiring')
      .sort((left, right) => lifecycleRank(right) - lifecycleRank(left))
    const reviveCount = Math.min(target - active.length, retiring.length)
    for (const slot of retiring.slice(0, reviveCount)) {
      slot.state = 'active'
      slot.retirementRemainingMs = null
    }

    active = this.activeFamilySlots()
    const missing = target - active.length
    for (const position of this.familyHydrationPositions(missing, target)) {
      const slot = this.activateFamily('reader', 0)
      if (slot === null) break
      if (position.stage === null) {
        this.positionFamilyAtRouteFraction(slot, position.phase)
      } else {
        slot.stage = position.stage
        slot.phase = position.phase
      }
    }
  }

  private familyHydrationPositions(
    count: number,
    target: number,
  ): readonly { stage: 'queue1' | 'queue2' | null; phase: number }[] {
    if (count <= 0) return []

    const selected: { stage: 'queue1' | 'queue2' | null; phase: number }[] = []
    const queueTargets = [
      { stage: 'queue1' as const, target: this.queue1DepthTarget },
      { stage: 'queue2' as const, target: this.queue2DepthTarget },
    ]
    for (const queue of queueTargets) {
      const resident = this.familySlots().filter(
        (slot) => slot.stage === queue.stage,
      ).length
      const needed = Math.min(
        count - selected.length,
        Math.max(0, queue.target - resident),
      )
      for (const phase of this.queueHydrationPhases(
        queue.stage,
        needed,
        queue.target,
      )) {
        selected.push({ stage: queue.stage, phase })
      }
    }

    const remaining = count - selected.length
    for (const fraction of this.flowHydrationFractions(remaining, target)) {
      selected.push({ stage: null, phase: fraction })
    }
    return selected
  }

  private flowHydrationFractions(
    count: number,
    target: number,
  ): readonly number[] {
    if (count <= 0) return []

    const routeLength = FLOW_TRAVEL_STAGES.reduce(
      (sum, stage) => sum + Math.max(1, this.stageTravelLengths[stage]),
      0,
    )
    const occupied = this.activeFamilySlots().map((slot) => {
      const stageIndex = FLOW_TRAVEL_STAGES.indexOf(slot.stage)
      if (stageIndex < 0) return 1
      const preceding = FLOW_TRAVEL_STAGES
        .slice(0, stageIndex)
        .reduce(
          (sum, stage) => sum + Math.max(1, this.stageTravelLengths[stage]),
          0,
        )
      return (preceding + slot.phase * Math.max(
        1,
        this.stageTravelLengths[slot.stage],
      )) / routeLength
    })
    const candidates = Array.from(
      { length: Math.max(1, target) },
      (_, index) => evenPhase(index, Math.max(1, target)),
    )
    const selected: number[] = []

    while (selected.length < count && candidates.length > 0) {
      let bestIndex = 0
      let bestDistance = -1
      for (let index = 0; index < candidates.length; index += 1) {
        const candidate = candidates[index]
        const distance = occupied.length === 0
          ? 1
          : Math.min(...occupied.map((fraction) => Math.abs(fraction - candidate)))
        if (distance > bestDistance) {
          bestIndex = index
          bestDistance = distance
        }
      }
      const [fraction] = candidates.splice(bestIndex, 1)
      occupied.push(fraction)
      selected.push(fraction)
    }

    return selected
  }

  private queueHydrationPhases(
    stage: 'queue1' | 'queue2',
    count: number,
    target: number,
  ): readonly number[] {
    if (count <= 0) return []

    const occupied = this.slots
      .filter((slot) =>
        slot.state !== 'inactive' &&
        slot.stage === stage
      )
      .map((slot) => slot.phase)
    const candidates = Array.from(
      { length: Math.max(1, target) },
      (_, index) => evenPhase(index, Math.max(1, target)),
    )
    const selected: number[] = []

    while (selected.length < count && candidates.length > 0) {
      let bestIndex = 0
      let bestDistance = -1
      for (let index = 0; index < candidates.length; index += 1) {
        const candidate = candidates[index]
        const distance = occupied.length === 0
          ? 1
          : Math.min(...occupied.map((phase) => Math.abs(phase - candidate)))
        if (distance > bestDistance) {
          bestIndex = index
          bestDistance = distance
        }
      }
      const [phase] = candidates.splice(bestIndex, 1)
      occupied.push(phase)
      selected.push(phase)
    }

    return selected
  }

  private positionFamilyAtRouteFraction(
    slot: MutableMarkerSlot,
    fraction: number,
  ): void {
    const totalLength = FLOW_TRAVEL_STAGES.reduce(
      (sum, stage) => sum + Math.max(1, this.stageTravelLengths[stage]),
      0,
    )
    let remainingDistance = clamp(fraction, 0, 1) * totalLength
    for (const stage of FLOW_TRAVEL_STAGES) {
      const stageLength = Math.max(1, this.stageTravelLengths[stage])
      if (remainingDistance <= stageLength) {
        slot.stage = stage
        slot.phase = remainingDistance / stageLength
        return
      }
      remainingDistance -= stageLength
    }
    slot.stage = 'http'
    slot.phase = 1
  }

  private activateFamily(
    stage: MarkerStage,
    phase: number,
  ): MutableMarkerSlot | null {
    const slot = this.inactiveSlot()
    if (slot === null) return null

    this.familySequence += 1
    slot.familyId = `transaction-family-${this.familySequence}`
    slot.state = 'active'
    slot.stage = stage
    slot.phase = clamp(phase, 0, 1)
    slot.retirementRemainingMs = null
    slot.outcome = null
    slot.pulseProgress = 0
    return slot
  }

  private inactiveSlot(): MutableMarkerSlot | null {
    return this.slots.find((slot) => slot.state === 'inactive') ?? null
  }

  private retireSlot(slot: MutableMarkerSlot): void {
    if (slot.state !== 'active') return
    slot.state = 'retiring'
    slot.retirementRemainingMs = RETIREMENT_GRACE_MS
  }

  private valveReleaseSlots(deltaMs: number): ReadonlySet<string> {
    if (this.runState !== 'running') return new Set()
    if (this.valveOpeningIndex === 0) {
      this.valveAdmissionElapsedMs = 0
      return new Set()
    }
    const waiting = this.waitingValveSlots()
    if (this.valveOpeningIndex === VALVE_OPENING_MAX_INDEX) {
      return new Set(waiting.map((slot) => slot.slotId))
    }
    if (waiting.length === 0) {
      this.valveAdmissionElapsedMs = 0
      return new Set()
    }

    const intervalMs = valveAdmissionIntervalMs(this.valveOpeningIndex)
    this.valveAdmissionElapsedMs = Math.min(
      intervalMs,
      this.valveAdmissionElapsedMs + deltaMs,
    )
    const releaseCount = Math.min(
      waiting.length,
      Math.floor(this.valveAdmissionElapsedMs / intervalMs),
      1,
    )
    if (releaseCount === 0) return new Set()

    this.valveAdmissionElapsedMs -= releaseCount * intervalMs
    return new Set(
      waiting
        .slice(0, releaseCount)
        .map((slot) => slot.slotId),
    )
  }

  private waitingValveSlots(): MutableMarkerSlot[] {
    return this.familySlots()
      .filter((slot) =>
        slot.stage === 'throttler' &&
        slot.phase <= this.valvePreAdmissionStopPhase &&
        this.atPreValveStop(slot)
      )
      .sort((left, right) =>
        right.phase - left.phase ||
        this.familyOrdinal(left) - this.familyOrdinal(right)
      )
  }

  private familyOrdinal(slot: MutableMarkerSlot): number {
    const ordinal = Number(slot.familyId?.split('-').at(-1))
    return Number.isFinite(ordinal) ? ordinal : Number.MAX_SAFE_INTEGER
  }

  private remapValvePositions(
    nextOpeningIndex: number,
    nextStopPhase: number,
    nextExitPhase: number,
  ): void {
    if (!this.initialized) return
    const waiting = this.waitingValveSlots()
    waiting.forEach((slot, index) => {
      slot.phase = Math.max(
        0.04,
        nextStopPhase - index * PRE_VALVE_FAMILY_SPACING,
      )
    })
    if (nextOpeningIndex !== 0) return
    this.familySlots()
      .filter((slot) =>
        slot.stage === 'throttler' &&
        slot.phase > this.valvePreAdmissionStopPhase &&
        slot.phase < nextExitPhase
      )
      .forEach((slot) => {
        slot.phase = nextExitPhase
      })
  }

  private atPreValveStop(slot: MutableMarkerSlot): boolean {
    return Math.abs(slot.phase - this.preValveStopPhase(slot)) < 1e-9
  }

  private advanceFamily(
    slot: MutableMarkerSlot,
    deltaMs: number,
    valveReleaseGranted: boolean,
  ): boolean {
    if (slot.stage === 'target' && slot.phase >= 1) return false
    if (
      slot.stage === 'queue1' && !this.queue1DequeueActive
    ) {
      return false
    }

    let changed = false
    let remainingDistance = MARKER_SPEED * deltaMs / 1_000
    while (remainingDistance > 0) {
      const travelLength = Math.max(1, this.stageTravelLengths[slot.stage])
      if (
        slot.stage === 'throttler' &&
        this.valveOpeningIndex < VALVE_OPENING_MAX_INDEX &&
        !valveReleaseGranted &&
        slot.phase <= this.valvePreAdmissionStopPhase
      ) {
        const stopPhase = this.preValveStopPhase(slot)
        const distanceToStop = Math.max(0, stopPhase - slot.phase) * travelLength
        if (distanceToStop === 0) return changed
        if (remainingDistance < distanceToStop) {
          slot.phase += remainingDistance / travelLength
        } else {
          slot.phase = stopPhase
        }
        return true
      }
      const distanceToEnd = Math.max(0, 1 - slot.phase) * travelLength
      if (remainingDistance < distanceToEnd) {
        slot.phase += remainingDistance / travelLength
        return true
      }

      remainingDistance -= distanceToEnd
      if (slot.phase < 1) changed = true
      slot.phase = 1
      const followingStage = nextStage(slot.stage)
      if (followingStage === null) return changed
      if (!this.canCrossBoundary(slot.stage, followingStage)) return changed

      slot.stage = followingStage
      slot.phase = 0
      changed = true
      if (slot.stage === 'queue1' && !this.queue1DequeueActive) {
        return changed
      }
    }
    return changed
  }

  private preValveStopPhase(slot: MutableMarkerSlot): number {
    const familiesAhead = this.familySlots().filter((candidate) =>
      candidate !== slot &&
      candidate.stage === 'throttler' &&
      candidate.phase > slot.phase &&
      candidate.phase <= this.valvePreAdmissionStopPhase
    ).length
    return Math.max(
      0.04,
      this.valvePreAdmissionStopPhase -
        familiesAhead * PRE_VALVE_FAMILY_SPACING,
    )
  }

  private canCrossBoundary(
    stage: MarkerStage,
    followingStage: MarkerStage,
  ): boolean {
    if (stage === 'reader' && followingStage === 'queue1') {
      return this.queue1DequeueActive
    }
    if (stage === 'queue1') return this.queue1DequeueActive
    return true
  }

  private advanceOutcomeDwell(
    slot: MutableMarkerSlot,
    deltaMs: number,
  ): boolean {
    slot.pulseProgress = clamp(
      slot.pulseProgress + deltaMs / OUTCOME_PULSE_MS,
      0,
      1,
    )
    if (
      slot.pulseProgress >= 1
    ) {
      this.completeFamilyLifecycle(slot)
    }
    return true
  }

  private completeFamilyLifecycle(slot: MutableMarkerSlot): void {
    if (
      slot.state === 'retiring' ||
      !this.throughputCadenceActive ||
      this.familyTarget === 0 ||
      this.activeFamilySlots().length > this.familyTarget
    ) {
      this.deactivateSlot(slot)
      return
    }

    this.familySequence += 1
    slot.familyId = `transaction-family-${this.familySequence}`
    slot.stage = 'reader'
    slot.phase = 0
    slot.retirementRemainingMs = null
    slot.outcome = null
    slot.pulseProgress = 0
  }

  private deactivateSlot(slot: MutableMarkerSlot): void {
    slot.familyId = null
    slot.state = 'inactive'
    slot.stage = 'reader'
    slot.phase = 0
    slot.retirementRemainingMs = null
    slot.outcome = null
    slot.pulseProgress = 0
  }

  private activeFamilySlots(): MutableMarkerSlot[] {
    return this.slots.filter((slot) => slot.state === 'active')
  }

  private familySlots(): MutableMarkerSlot[] {
    return this.slots.filter((slot) => slot.state !== 'inactive')
  }

  private captureOutcomes(telemetry: MarkerLifecycleTelemetry): void {
    const successDelta = Math.max(
      0,
      telemetry.http.requestsSucceededTotal - this.previousRequestsSucceededTotal,
    )
    const failureDelta = Math.max(
      0,
      telemetry.http.requestsFailedTotal - this.previousRequestsFailedTotal,
    )
    const completedDelta = Math.max(
      0,
      telemetry.http.requestsCompletedTotal - this.previousRequestsCompletedTotal,
    )
    const unclassified = Math.max(0, completedDelta - successDelta - failureDelta)
    const successCount = successDelta + (telemetry.http.connectionError ? 0 : unclassified)
    const errorCount = failureDelta + (telemetry.http.connectionError ? unclassified : 0)

    const available = MAX_PENDING_OUTCOMES - this.pendingOutcomes.length
    for (let index = 0; index < Math.min(errorCount, available); index += 1) {
      this.pendingOutcomes.push('error')
    }
    const remaining = MAX_PENDING_OUTCOMES - this.pendingOutcomes.length
    for (let index = 0; index < Math.min(successCount, remaining); index += 1) {
      this.pendingOutcomes.push('success')
    }
  }

  private assignPendingOutcomes(): boolean {
    const candidates = this.familySlots()
      .filter((slot) =>
        slot.outcome === null &&
        slot.stage === 'target' &&
        slot.phase >= 1
      )
      .sort((left, right) => lifecycleRank(right) - lifecycleRank(left))

    let changed = false

    for (const slot of candidates) {
      const outcome = this.pendingOutcomes.shift()
      if (outcome === undefined) break
      slot.outcome = outcome
      changed = true
    }
    return changed
  }

  private assignConnectionErrors(): boolean {
    let changed = false
    this.familySlots()
      .filter((slot) =>
        slot.outcome === null &&
        slot.stage === 'target' &&
        slot.phase >= 1
      )
      .forEach((slot) => {
        slot.outcome = 'error'
        changed = true
      })
    return changed
  }

  private exposeReducedMotionArrivals(): boolean {
    const waitingAtTarget = this.familySlots().filter((slot) =>
      slot.outcome === null && slot.stage === 'target' && slot.phase >= 1
    ).length
    const requiredArrivals = Math.max(
      this.pendingOutcomes.length,
      this.connectionError ? 1 : 0,
    )
    const missing = Math.max(0, requiredArrivals - waitingAtTarget)
    if (missing === 0) return false

    const completedHttp = this.activeFamilySlots()
      .filter((slot) =>
        slot.outcome === null && !this.familyHeldAtStoppedQueue(slot)
      )
      .sort((left, right) => lifecycleRank(right) - lifecycleRank(left))
      .slice(0, missing)
    for (const slot of completedHttp) {
      slot.stage = 'target'
      slot.phase = 1
    }
    return completedHttp.length > 0
  }

  private familyHeldAtStoppedQueue(slot: MutableMarkerSlot): boolean {
    return (slot.stage === 'reader' && !this.queue1DequeueActive && slot.phase >= 1) ||
      (slot.stage === 'queue1' && !this.queue1DequeueActive) ||
      (slot.stage === 'throttler' &&
        this.valveOpeningIndex < VALVE_OPENING_MAX_INDEX &&
        slot.phase <= this.valvePreAdmissionStopPhase)
  }

  private captureTotals(telemetry: MarkerLifecycleTelemetry): void {
    this.previousQueue1EnqueuedTotal = telemetry.queue1.enqueuedBatchesTotal
    this.previousQueue1DequeuedTotal = telemetry.queue1.dequeuedBatchesTotal
    this.previousQueue2EnqueuedTotal = telemetry.queue2.enqueuedBatchesTotal
    this.previousQueue2DequeuedTotal = telemetry.queue2.dequeuedBatchesTotal
    this.previousRequestsStartedTotal = telemetry.http.requestsStartedTotal
    this.previousRequestsCompletedTotal = telemetry.http.requestsCompletedTotal
    this.previousRequestsSucceededTotal = telemetry.http.requestsSucceededTotal
    this.previousRequestsFailedTotal = telemetry.http.requestsFailedTotal
  }

  private reset(): void {
    this.slots.forEach((slot) => this.deactivateSlot(slot))
    this.pendingOutcomes.length = 0
    this.familyTarget = 0
    this.throughputCadenceActive = false
    this.valveAdmissionElapsedMs = 0
    this.motionElapsedMs = 0
    this.reducedMotionElapsedMs = 0
    this.initialized = false
  }

  private queued(slot: MutableMarkerSlot): boolean {
    return (slot.stage === 'reader' && !this.queue1DequeueActive && slot.phase >= 1) ||
      (slot.stage === 'queue1' && !this.queue1DequeueActive) ||
      (slot.stage === 'throttler' &&
        this.valveOpeningIndex < VALVE_OPENING_MAX_INDEX &&
        slot.phase <= this.valvePreAdmissionStopPhase)
  }

  private publish(): void {
    this.revision += 1
    this.snapshot = Object.freeze({
      revision: this.revision,
      reducedMotion: this.reducedMotion,
      motionElapsedMs: this.motionElapsedMs,
      markers: Object.freeze(this.slots.map((slot) => Object.freeze({
        slotId: slot.slotId,
        familyId: slot.familyId,
        state: slot.state,
        stage: slot.stage,
        phase: slot.phase,
        queued: this.queued(slot),
        outcome: slot.outcome,
        outcomeVisible:
          slot.outcome !== null && slot.stage === 'target' && slot.phase >= 1,
        pulseProgress: slot.pulseProgress,
      }))),
    })
    this.listeners.forEach((listener) => listener())
  }
}
