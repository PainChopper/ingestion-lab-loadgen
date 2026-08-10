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

const MARKER_SPEED = 70
const FLOW_MARKER_SCALE_TPS = 250_000
const OUTCOME_PULSE_MS = 520
const RETIREMENT_GRACE_MS = 1_000

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
  readonly queue1: QueueMarkerTelemetry
  readonly queue2: QueueMarkerTelemetry
  readonly http: HttpMarkerTelemetry
  readonly stageTravelLengths: Readonly<Record<MarkerStage, number>>
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
      !this.throughputCadenceActive ||
      !Number.isFinite(deltaMs) ||
      deltaMs <= 0
    ) {
      return
    }

    let changed = false
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
      if (this.runState !== 'running' || this.reducedMotion) continue
      if (!this.throughputCadenceActive) continue

      changed = this.advanceFamily(slot, deltaMs) || changed
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

  private advanceFamily(slot: MutableMarkerSlot, deltaMs: number): boolean {
    if (slot.stage === 'target' && slot.phase >= 1) return false
    if (
      (slot.stage === 'queue1' && !this.queue1DequeueActive) ||
      (slot.stage === 'queue2' && !this.queue2DequeueActive)
    ) {
      return false
    }

    let changed = false
    let remainingDistance = MARKER_SPEED * deltaMs / 1_000
    while (remainingDistance > 0) {
      const travelLength = Math.max(1, this.stageTravelLengths[slot.stage])
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
      if (
        (slot.stage === 'queue1' && !this.queue1DequeueActive) ||
        (slot.stage === 'queue2' && !this.queue2DequeueActive)
      ) {
        return changed
      }
    }
    return changed
  }

  private canCrossBoundary(
    stage: MarkerStage,
    followingStage: MarkerStage,
  ): boolean {
    if (stage === 'reader' && followingStage === 'queue1') {
      return this.queue1DequeueActive
    }
    if (stage === 'queue1') return this.queue1DequeueActive
    if (stage === 'throttler' && followingStage === 'queue2') {
      return this.queue2DequeueActive
    }
    if (stage === 'queue2') return this.queue2DequeueActive
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
      slot.pulseProgress >= 1 &&
      (
        this.throughputCadenceActive ||
        this.familyTarget === 0 ||
        slot.state === 'retiring'
      )
    ) {
      this.completeFamilyLifecycle(slot)
    }
    return true
  }

  private completeFamilyLifecycle(slot: MutableMarkerSlot): void {
    if (
      slot.state === 'retiring' ||
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
      (slot.stage === 'throttler' && !this.queue2DequeueActive && slot.phase >= 1) ||
      (slot.stage === 'queue2' && !this.queue2DequeueActive)
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
    this.initialized = false
  }

  private queued(slot: MutableMarkerSlot): boolean {
    return (slot.stage === 'reader' && !this.queue1DequeueActive && slot.phase >= 1) ||
      (slot.stage === 'queue1' && !this.queue1DequeueActive) ||
      (slot.stage === 'throttler' && !this.queue2DequeueActive && slot.phase >= 1) ||
      (slot.stage === 'queue2' && !this.queue2DequeueActive)
  }

  private publish(): void {
    this.revision += 1
    this.snapshot = Object.freeze({
      revision: this.revision,
      reducedMotion: this.reducedMotion,
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
