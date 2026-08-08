import type { QueueId, RunState } from '../../model/loadgen'
import {
  getQueueMarkerCount,
  QUEUE_CABLE_MAX_MARKERS,
} from './queueCableGeometry'

export const MAX_OCCUPANCY_MARKERS = QUEUE_CABLE_MAX_MARKERS
export const MAX_FLOW_MARKERS = 12
export const MAX_HTTP_ATTEMPT_MARKERS = 3

const QUEUE_MARKER_SPEED = 190
const FLOW_MARKER_SCALE_TPS = 250_000
const HTTP_MARKER_SPEED = 100
const HTTP_OUTCOME_PULSE_MS = 520
const HTTP_ARRIVAL_PHASE = 0.94
const MAX_RETIRING_OCCUPANCY_MARKERS = 2

export type QueueMarkerKind = 'occupancy' | 'flow'
export type MarkerSlotState = 'inactive' | 'active' | 'retiring'
export type HttpOutcome = 'success' | 'error'

export interface QueueMarkerSlotSnapshot {
  readonly slotId: string
  readonly familyId: string | null
  readonly queueId: QueueId
  readonly kind: QueueMarkerKind
  readonly state: MarkerSlotState
  readonly phase: number
  readonly queued: boolean
}

export interface HttpMarkerSlotSnapshot {
  readonly slotId: string
  readonly familyId: string | null
  readonly state: MarkerSlotState
  readonly phase: number
  readonly outcome: HttpOutcome | null
  readonly outcomeVisible: boolean
  readonly pulseProgress: number
}

export interface MarkerLifecycleSnapshot {
  readonly revision: number
  readonly reducedMotion: boolean
  readonly queue1: readonly QueueMarkerSlotSnapshot[]
  readonly queue2: readonly QueueMarkerSlotSnapshot[]
  readonly http: readonly HttpMarkerSlotSnapshot[]
}

export interface QueueMarkerTelemetry {
  readonly id: QueueId
  readonly depthBatches: number | null
  readonly appliedCapacity: number
  readonly throughputTps: number | null
  readonly flowActive: boolean
  readonly enqueuedBatchesTotal: number
  readonly dequeuedBatchesTotal: number
  readonly occupancyTravelLength: number
  readonly flowTravelLength: number
}

export interface HttpMarkerTelemetry {
  readonly inFlightRequests: number
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly travelLength: number
  readonly connectionError: boolean
}

export interface MarkerLifecycleTelemetry {
  readonly runState: RunState
  readonly reducedMotion: boolean
  readonly queue1: QueueMarkerTelemetry
  readonly queue2: QueueMarkerTelemetry
  readonly http: HttpMarkerTelemetry
}

interface MutableQueueMarkerSlot {
  readonly slotId: string
  readonly queueId: QueueId
  readonly kind: QueueMarkerKind
  familyId: string | null
  state: MarkerSlotState
  phase: number
  launchDelayMs: number
}

interface MutableQueuePool {
  readonly id: QueueId
  readonly slots: MutableQueueMarkerSlot[]
  initialized: boolean
  familySequence: number
  flowActive: boolean
  occupancyTravelLength: number
  flowTravelLength: number
  previousEnqueuedTotal: number
  previousDequeuedTotal: number
}

interface MutableHttpMarkerSlot {
  readonly slotId: string
  familyId: string | null
  state: MarkerSlotState
  phase: number
  outcome: HttpOutcome | null
  pulseProgress: number
}

type Listener = () => void

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function positiveInteger(value: number): number {
  return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0
}

export function getOccupancyMarkerTarget(
  depthBatches: number | null,
  appliedCapacity: number,
): number {
  return getQueueMarkerCount(
    depthBatches,
    appliedCapacity,
  )
}

export function getFlowMarkerTarget(throughputTps: number | null): number {
  if (throughputTps === null || !Number.isFinite(throughputTps)) return 0
  if (throughputTps <= 0) return 0

  return clamp(
    Math.ceil((throughputTps / FLOW_MARKER_SCALE_TPS) * MAX_FLOW_MARKERS),
    1,
    MAX_FLOW_MARKERS,
  )
}

function createQueueSlots(
  queueId: QueueId,
  kind: QueueMarkerKind,
  count: number,
): MutableQueueMarkerSlot[] {
  return Array.from({ length: count }, (_, ordinal) => ({
    slotId: `${queueId}-${kind}-${ordinal + 1}`,
    queueId,
    kind,
    familyId: null,
    state: 'inactive',
    phase: 0,
    launchDelayMs: 0,
  }))
}

function createQueuePool(id: QueueId): MutableQueuePool {
  return {
    id,
    slots: [
      ...createQueueSlots(id, 'occupancy', MAX_OCCUPANCY_MARKERS),
      ...createQueueSlots(id, 'flow', MAX_FLOW_MARKERS),
    ],
    initialized: false,
    familySequence: 0,
    flowActive: false,
    occupancyTravelLength: 1,
    flowTravelLength: 1,
    previousEnqueuedTotal: 0,
    previousDequeuedTotal: 0,
  }
}

function createHttpSlots(): MutableHttpMarkerSlot[] {
  return Array.from({ length: MAX_HTTP_ATTEMPT_MARKERS }, (_, ordinal) => ({
    slotId: `http-attempt-${ordinal + 1}`,
    familyId: null,
    state: 'inactive',
    phase: 0,
    outcome: null,
    pulseProgress: 0,
  }))
}

function activeSlots(
  pool: MutableQueuePool,
  kind: QueueMarkerKind,
): MutableQueueMarkerSlot[] {
  return pool.slots.filter(
    (slot) => slot.kind === kind && slot.state === 'active',
  )
}

function visibleSlots(
  pool: MutableQueuePool,
  kind: QueueMarkerKind,
): MutableQueueMarkerSlot[] {
  return pool.slots.filter(
    (slot) => slot.kind === kind && slot.state !== 'inactive',
  )
}

function activateQueueSlot(
  pool: MutableQueuePool,
  kind: QueueMarkerKind,
  phase: number,
  launchDelayMs = 0,
): MutableQueueMarkerSlot | null {
  const slot = pool.slots.find(
    (candidate) => candidate.kind === kind && candidate.state === 'inactive',
  )
  if (slot === undefined) return null

  pool.familySequence += 1
  slot.familyId = `${pool.id}-family-${pool.familySequence}`
  slot.state = 'active'
  slot.phase = clamp(phase, 0, 1)
  slot.launchDelayMs = Math.max(0, launchDelayMs)
  return slot
}

function deactivateQueueSlot(slot: MutableQueueMarkerSlot): void {
  slot.familyId = null
  slot.state = 'inactive'
  slot.phase = 0
  slot.launchDelayMs = 0
}

function resetQueuePool(pool: MutableQueuePool): void {
  pool.slots.forEach(deactivateQueueSlot)
  pool.initialized = false
  pool.flowActive = false
  pool.previousEnqueuedTotal = 0
  pool.previousDequeuedTotal = 0
}

function initializeOccupancy(
  pool: MutableQueuePool,
  target: number,
): void {
  for (let index = 0; index < target; index += 1) {
    const phase = target === 1 ? 0.5 : 0.2 + (index / (target - 1)) * 0.6
    activateQueueSlot(pool, 'occupancy', phase)
  }
  pool.initialized = true
}

function reconcileOccupancy(
  pool: MutableQueuePool,
  target: number,
): void {
  if (!pool.initialized) {
    initializeOccupancy(pool, target)
    return
  }

  const current = activeSlots(pool, 'occupancy')
  const retiring = visibleSlots(pool, 'occupancy')
    .filter((slot) => slot.state === 'retiring')
    .sort((left, right) => left.phase - right.phase)

  if (current.length < target) {
    const reviveCount = Math.min(target - current.length, retiring.length)
    for (let index = 0; index < reviveCount; index += 1) {
      const slot = retiring[index]
      slot.state = 'active'
      slot.launchDelayMs = 0
    }
    retiring
      .slice(reviveCount + MAX_RETIRING_OCCUPANCY_MARKERS)
      .forEach(deactivateQueueSlot)

    const activeCount = current.length + reviveCount
    const travelDurationMs =
      (pool.occupancyTravelLength / QUEUE_MARKER_SPEED) * 1000
    const launchSpacingMs = target > 0 ? travelDurationMs / target : 0
    for (let index = activeCount; index < target; index += 1) {
      activateQueueSlot(
        pool,
        'occupancy',
        0,
        (index - activeCount) * launchSpacingMs,
      )
    }
    return
  }

  if (current.length > target) {
    const excessSlots = current
      .slice()
      .sort((left, right) => right.slotId.localeCompare(left.slotId))
      .slice(0, current.length - target)
    const retireBudget = Math.max(
      0,
      MAX_RETIRING_OCCUPANCY_MARKERS - retiring.length,
    )

    excessSlots.forEach((slot, index) => {
      if (index < retireBudget) {
        slot.state = 'retiring'
        slot.launchDelayMs = 0
      } else {
        deactivateQueueSlot(slot)
      }
    })
  }

  retiring
    .slice(MAX_RETIRING_OCCUPANCY_MARKERS)
    .forEach(deactivateQueueSlot)
}

function reconcileFlow(pool: MutableQueuePool, desiredCount: number): void {
  const current = activeSlots(pool, 'flow')
  if (current.length === desiredCount) return

  if (current.length < desiredCount) {
    const retiring = visibleSlots(pool, 'flow')
      .filter((slot) => slot.state === 'retiring')
    const reviveCount = Math.min(desiredCount - current.length, retiring.length)
    for (let index = 0; index < reviveCount; index += 1) {
      retiring[index].state = 'active'
      retiring[index].launchDelayMs = 0
    }

    for (
      let index = current.length + reviveCount;
      index < desiredCount;
      index += 1
    ) {
      activateQueueSlot(pool, 'flow', 0)
    }
  }

  if (current.length > desiredCount) {
    current
      .slice(desiredCount)
      .forEach((slot) => {
        slot.state = 'retiring'
        slot.launchDelayMs = 0
      })
  }

  const active = activeSlots(pool, 'flow')
  if (active.length === 0) return

  const anchorPhase = current[0]?.phase ?? 0
  for (let index = 0; index < active.length; index += 1) {
    active[index].phase = (anchorPhase + index / active.length) % 1
  }
}

function queueTotalsReset(
  pool: MutableQueuePool,
  telemetry: QueueMarkerTelemetry,
): boolean {
  return telemetry.enqueuedBatchesTotal < pool.previousEnqueuedTotal ||
    telemetry.dequeuedBatchesTotal < pool.previousDequeuedTotal
}

function queueSnapshot(pool: MutableQueuePool): readonly QueueMarkerSlotSnapshot[] {
  return Object.freeze(pool.slots.map((slot) => Object.freeze({
    slotId: slot.slotId,
    familyId: slot.familyId,
    queueId: slot.queueId,
    kind: slot.kind,
    state: slot.state,
    phase: slot.phase,
    queued: slot.kind === 'occupancy' && !pool.flowActive,
  })))
}

export class MarkerLifecycleController {
  private readonly listeners = new Set<Listener>()
  private readonly queue1 = createQueuePool('reader-to-throttler')
  private readonly queue2 = createQueuePool('throttler-to-sender')
  private readonly httpSlots = createHttpSlots()
  private readonly pendingOutcomes: HttpOutcome[] = []
  private revision = 0
  private familySequence = 0
  private runState: RunState = 'idle'
  private reducedMotion = false
  private httpTravelLength = 1
  private previousRequestsStartedTotal = 0
  private previousRequestsCompletedTotal = 0
  private previousRequestsSucceededTotal = 0
  private previousRequestsFailedTotal = 0
  private initializedHttp = false
  private snapshot: MarkerLifecycleSnapshot

  constructor(telemetry: MarkerLifecycleTelemetry) {
    this.snapshot = Object.freeze({
      revision: 0,
      reducedMotion: telemetry.reducedMotion,
      queue1: Object.freeze([]),
      queue2: Object.freeze([]),
      http: Object.freeze([]),
    })
    this.reconcile(telemetry)
  }

  readonly subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  readonly getSnapshot = (): MarkerLifecycleSnapshot => this.snapshot

  reconcile(telemetry: MarkerLifecycleTelemetry): void {
    this.runState = telemetry.runState
    this.reducedMotion = telemetry.reducedMotion
    this.reconcileQueue(this.queue1, telemetry.queue1)
    this.reconcileQueue(this.queue2, telemetry.queue2)
    this.reconcileHttp(telemetry.http)
    this.publish()
  }

  advance(deltaMs: number): void {
    if (
      this.runState !== 'running' ||
      this.reducedMotion ||
      !Number.isFinite(deltaMs) ||
      deltaMs <= 0
    ) {
      return
    }

    const queue1Changed = this.advanceQueue(this.queue1, deltaMs)
    const queue2Changed = this.advanceQueue(this.queue2, deltaMs)
    const httpChanged = this.advanceHttp(deltaMs)
    if (queue1Changed || queue2Changed || httpChanged) this.publish()
  }

  private reconcileQueue(
    pool: MutableQueuePool,
    telemetry: QueueMarkerTelemetry,
  ): void {
    if (queueTotalsReset(pool, telemetry)) resetQueuePool(pool)

    pool.occupancyTravelLength = Math.max(1, telemetry.occupancyTravelLength)
    pool.flowTravelLength = Math.max(1, telemetry.flowTravelLength)
    const occupancyTarget = getOccupancyMarkerTarget(
      telemetry.depthBatches,
      telemetry.appliedCapacity,
    )
    reconcileOccupancy(pool, occupancyTarget)

    if (this.runState !== 'paused') {
      pool.flowActive = this.runState === 'running' && telemetry.flowActive
      const desiredFlowCount = pool.flowActive
        ? getFlowMarkerTarget(telemetry.throughputTps)
        : 0
      reconcileFlow(pool, desiredFlowCount)
    }

    pool.previousEnqueuedTotal = telemetry.enqueuedBatchesTotal
    pool.previousDequeuedTotal = telemetry.dequeuedBatchesTotal
  }

  private advanceQueue(pool: MutableQueuePool, deltaMs: number): boolean {
    let changed = false

    for (const slot of pool.slots) {
      if (slot.state === 'inactive') continue

      const shouldAdvance = slot.state === 'retiring' ||
        slot.kind !== 'occupancy' ||
        pool.flowActive
      if (!shouldAdvance) continue

      const delayConsumed = Math.min(slot.launchDelayMs, deltaMs)
      slot.launchDelayMs -= delayConsumed
      const movingMs = deltaMs - delayConsumed
      if (movingMs <= 0) {
        changed = true
        continue
      }

      const travelLength = slot.kind === 'occupancy'
        ? pool.occupancyTravelLength
        : pool.flowTravelLength
      slot.phase += (QUEUE_MARKER_SPEED * movingMs) / (travelLength * 1000)
      changed = true
      if (slot.phase < 1) continue

      if (slot.state === 'retiring') {
        deactivateQueueSlot(slot)
      } else {
        slot.phase %= 1
        pool.familySequence += 1
        slot.familyId = `${pool.id}-family-${pool.familySequence}`
      }
    }

    return changed
  }

  private reconcileHttp(telemetry: HttpMarkerTelemetry): void {
    const totalsReset = this.initializedHttp && (
      telemetry.requestsStartedTotal < this.previousRequestsStartedTotal ||
      telemetry.requestsCompletedTotal < this.previousRequestsCompletedTotal ||
      telemetry.requestsSucceededTotal < this.previousRequestsSucceededTotal ||
      telemetry.requestsFailedTotal < this.previousRequestsFailedTotal
    )
    if (totalsReset) this.resetHttp()

    this.httpTravelLength = Math.max(1, telemetry.travelLength)
    if (!this.initializedHttp) {
      this.initializedHttp = true
      const initialCount = clamp(
        positiveInteger(telemetry.inFlightRequests),
        0,
        MAX_HTTP_ATTEMPT_MARKERS,
      )
      for (let index = 0; index < initialCount; index += 1) {
        this.activateHttpSlot(index * 0.08)
      }
    } else {
      const startedDelta = Math.max(
        0,
        telemetry.requestsStartedTotal - this.previousRequestsStartedTotal,
      )
      const successDelta = Math.max(
        0,
        telemetry.requestsSucceededTotal - this.previousRequestsSucceededTotal,
      )
      const failureDelta = Math.max(
        0,
        telemetry.requestsFailedTotal - this.previousRequestsFailedTotal,
      )
      const completedDelta = Math.max(
        0,
        telemetry.requestsCompletedTotal - this.previousRequestsCompletedTotal,
      )
      const unclassifiedCompletions = Math.max(
        0,
        completedDelta - successDelta - failureDelta,
      )
      this.enqueueOutcomes(
        successDelta + (telemetry.connectionError ? 0 : unclassifiedCompletions),
        failureDelta + (telemetry.connectionError ? unclassifiedCompletions : 0),
      )

      if (this.runState === 'running' && startedDelta > 0) {
        const desired = clamp(
          Math.max(1, positiveInteger(telemetry.inFlightRequests)),
          1,
          MAX_HTTP_ATTEMPT_MARKERS,
        )
        this.ensureHttpSlots(desired)
      }
    }

    if (
      this.runState === 'running' &&
      telemetry.inFlightRequests > 0 &&
      this.httpSlots.every((slot) => slot.state === 'inactive')
    ) {
      this.activateHttpSlot(0)
    }

    this.assignPendingOutcomes()
    if (telemetry.connectionError) {
      this.httpSlots
        .filter((slot) => slot.state !== 'inactive' && slot.outcome === null)
        .forEach((slot) => {
          slot.outcome = 'error'
        })
    }

    if (this.reducedMotion) {
      this.httpSlots
        .filter((slot) => slot.state !== 'inactive' && slot.outcome !== null)
        .forEach((slot) => {
          slot.phase = Math.max(slot.phase, HTTP_ARRIVAL_PHASE)
        })
    }

    this.previousRequestsStartedTotal = telemetry.requestsStartedTotal
    this.previousRequestsCompletedTotal = telemetry.requestsCompletedTotal
    this.previousRequestsSucceededTotal = telemetry.requestsSucceededTotal
    this.previousRequestsFailedTotal = telemetry.requestsFailedTotal
  }

  private enqueueOutcomes(successCount: number, failureCount: number): void {
    let remaining = MAX_HTTP_ATTEMPT_MARKERS - this.pendingOutcomes.length
    const add = (outcome: HttpOutcome, count: number) => {
      const accepted = Math.min(remaining, positiveInteger(count))
      for (let index = 0; index < accepted; index += 1) {
        this.pendingOutcomes.push(outcome)
      }
      remaining -= accepted
    }

    add('error', failureCount)
    add('success', successCount)
  }

  private ensureHttpSlots(desired: number): void {
    const current = this.httpSlots.filter((slot) => slot.state !== 'inactive').length
    for (let index = current; index < desired; index += 1) {
      if (this.activateHttpSlot(index * 0.08) === null) return
    }
  }

  private activateHttpSlot(phase: number): MutableHttpMarkerSlot | null {
    const slot = this.httpSlots.find((candidate) => candidate.state === 'inactive')
    if (slot === undefined) return null

    this.familySequence += 1
    slot.familyId = `http-family-${this.familySequence}`
    slot.state = 'active'
    slot.phase = clamp(phase, 0, HTTP_ARRIVAL_PHASE)
    slot.outcome = null
    slot.pulseProgress = 0
    return slot
  }

  private assignPendingOutcomes(): void {
    const candidates = this.httpSlots
      .filter((slot) => slot.state !== 'inactive' && slot.outcome === null)
      .sort((left, right) => right.phase - left.phase)

    for (const slot of candidates) {
      const outcome = this.pendingOutcomes.shift()
      if (outcome === undefined) break
      slot.outcome = outcome
    }

    while (
      this.pendingOutcomes.length > 0 &&
      this.httpSlots.some((slot) => slot.state === 'inactive')
    ) {
      const slot = this.activateHttpSlot(0)
      if (slot === null) break
      slot.outcome = this.pendingOutcomes.shift() ?? null
    }
  }

  private advanceHttp(deltaMs: number): boolean {
    const phaseDelta = (HTTP_MARKER_SPEED * deltaMs) / (this.httpTravelLength * 1000)
    let changed = false

    for (const slot of this.httpSlots) {
      if (slot.state === 'inactive') continue

      if (slot.phase < HTTP_ARRIVAL_PHASE) {
        slot.phase = Math.min(HTTP_ARRIVAL_PHASE, slot.phase + phaseDelta)
        changed = true
      }

      if (slot.phase < HTTP_ARRIVAL_PHASE || slot.outcome === null) continue

      slot.state = 'retiring'
      slot.pulseProgress = clamp(
        slot.pulseProgress + deltaMs / HTTP_OUTCOME_PULSE_MS,
        0,
        1,
      )
      changed = true
      if (slot.pulseProgress >= 1) this.deactivateHttpSlot(slot)
    }

    return changed
  }

  private deactivateHttpSlot(slot: MutableHttpMarkerSlot): void {
    slot.familyId = null
    slot.state = 'inactive'
    slot.phase = 0
    slot.outcome = null
    slot.pulseProgress = 0
  }

  private resetHttp(): void {
    this.httpSlots.forEach((slot) => this.deactivateHttpSlot(slot))
    this.pendingOutcomes.length = 0
    this.initializedHttp = false
  }

  private publish(): void {
    this.revision += 1
    this.snapshot = Object.freeze({
      revision: this.revision,
      reducedMotion: this.reducedMotion,
      queue1: queueSnapshot(this.queue1),
      queue2: queueSnapshot(this.queue2),
      http: Object.freeze(this.httpSlots.map((slot) => Object.freeze({
        slotId: slot.slotId,
        familyId: slot.familyId,
        state: slot.state,
        phase: slot.phase,
        outcome: slot.outcome,
        outcomeVisible: slot.outcome !== null && slot.phase >= HTTP_ARRIVAL_PHASE,
        pulseProgress: slot.pulseProgress,
      }))),
    })
    this.listeners.forEach((listener) => listener())
  }
}
