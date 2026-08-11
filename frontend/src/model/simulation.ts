import type {
  HttpLastOutcome,
  QueueTrend,
  SenderWorkerStateCounts,
  ThrottlerInstallationMode,
} from './loadgen'

export const FIXED_STEP_MS = 10
export const SNAPSHOT_INTERVAL_MS = 100
export const RETRY_MAX_ATTEMPTS = 3
export const RETRY_BACKOFF_BASE_MS = 250
export const RETRY_BACKOFF_MULTIPLIER = 2
export const RETRY_JITTER_PERCENT = 20
export const RETRYABLE_STATUS_CODES = Object.freeze([
  408,
  425,
  429,
  500,
  502,
  503,
  504,
])

const RATE_WINDOW_STEPS = 100
const READER_TRANSACTIONS_PER_WORKER_SECOND = 50_000
const RETRY_JITTER_FACTORS = [0.8, 0.9, 1, 1.1, 1.2] as const

export interface SimulationConfig {
  readerWorkers: number
  senderWorkers: number
  requestedTps: number
  throttlerInstallationMode: ThrottlerInstallationMode
  readBatchSize: number
  httpBatchSize: number
  httpTimeoutMs: number
  targetDelayMs: number
  targetErrorRatePercent: number
}

export interface CapacityTelemetry {
  readonly applied: number
  readonly preview: number | null
  readonly pending: number | null
}

export interface QueueTelemetry {
  readonly capacity: CapacityTelemetry
  readonly enqueuedBatchesTotal: number
  readonly enqueuedTransactionsTotal: number
  readonly dequeuedBatchesTotal: number
  readonly dequeuedTransactionsTotal: number
  readonly depthBatches: number
  readonly queuedTransactions: number
  readonly handoffBatches: number
  readonly handoffBatchesTotal: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly blockedMsTotal: number
  readonly inputBatchesPerSecond: number
  readonly outputBatchesPerSecond: number
  readonly inputTransactionsPerSecond: number
  readonly outputTransactionsPerSecond: number
  readonly trend: QueueTrend
}

export interface HttpTelemetry {
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly responsesRejectedTotal: number
  readonly requestsTimedOutTotal: number
  readonly networkErrorsTotal: number
  readonly successfulTransactionsTotal: number
  readonly failedAttemptTransactionsTotal: number
  readonly inFlightRequests: number
  readonly startedTransactionsPerSecond: number
  readonly completedTransactionsPerSecond: number
  readonly succeededTransactionsPerSecond: number
  readonly rejectedTransactionsPerSecond: number
  readonly latestStatusCode: number | null
  readonly lastOutcome: HttpLastOutcome
  readonly latestLatencyMs: number | null
  readonly latestResponseLatencyMs: number | null
}

export interface SenderTelemetry {
  readonly workers: CapacityTelemetry
  readonly workerStates: SenderWorkerStateCounts
  readonly workerSlots: readonly SenderWorkerSlotTelemetry[]
  readonly retryAttemptsStartedTotal: number
  readonly retryAttemptedTransactionsPerSecond: number
  readonly terminalFailedTransactionsPerSecond: number
  readonly terminalFailedBatchesTotal: number
  readonly terminalFailedTransactionsTotal: number
  readonly ambiguousTimeoutTransactionsTotal: number
  readonly duplicateRiskTransactionsTotal: number
  readonly ambiguousTerminalTransactionsTotal: number
}

export interface SenderWorkerSlotTelemetry {
  readonly id: string
  readonly ordinal: number
  readonly state: SenderWorkerState
}

export interface SimulationTelemetry {
  readonly elapsedMs: number
  readonly totalTransactions: number
  readonly limitedMs: number
  readonly readerCapacityTps: number
  readonly readerTransactionsPerSecond: number
  readonly admittedTransactionsPerSecond: number
  readonly attemptedTransactionsPerSecond: number
  readonly acceptedTransactionsPerSecond: number
  readonly rejectedTransactionsPerSecond: number
  readonly queue1: QueueTelemetry
  readonly queue2: QueueTelemetry
  readonly sender: SenderTelemetry
  readonly http: HttpTelemetry
}

export interface SimulationBatch {
  readonly sequence: number
  readonly identity: string
  readonly transactions: number
}

export interface SimulationAttemptContext {
  readonly batch: SimulationBatch
  readonly attempt: number
  readonly startedAtMs: number
  readonly targetDelayMs: number
  readonly targetErrorRatePercent: number
  readonly httpTimeoutMs: number
}

export type SimulationAttemptOutcome =
  | {
      readonly kind: 'http-response'
      readonly statusCode: number
      readonly latencyMs: number
    }
  | {
      readonly kind: 'timeout'
      readonly latencyMs: number
    }
  | {
      readonly kind: 'network-error'
      readonly latencyMs: number
    }

export type SimulationAttemptOutcomeSource = (
  context: SimulationAttemptContext,
) => SimulationAttemptOutcome

interface QueueActivity {
  inputBatches: number
  inputTransactions: number
  outputBatches: number
  outputTransactions: number
  handoffBatches: number
}

interface StepActivity {
  queue1: QueueActivity
  queue2: QueueActivity
  httpStartedTransactions: number
  httpRetryStartedTransactions: number
  httpCompletedTransactions: number
  httpSucceededTransactions: number
  httpRejectedTransactions: number
  terminalFailedTransactions: number
}

type SenderWorkerState = 'idle' | 'in-flight' | 'backoff'

interface SenderWorker {
  readonly id: string
  readonly ordinal: number
  retiring: boolean
  state: SenderWorkerState
  batch: SimulationBatch | null
  attempt: number
  completeAtMs: number
  retryAtMs: number
  outcome: SimulationAttemptOutcome | null
  attemptLatencyMs: number
  hadAmbiguousOutcome: boolean
}

function createQueueActivity(): QueueActivity {
  return {
    inputBatches: 0,
    inputTransactions: 0,
    outputBatches: 0,
    outputTransactions: 0,
    handoffBatches: 0,
  }
}

function createStepActivity(): StepActivity {
  return {
    queue1: createQueueActivity(),
    queue2: createQueueActivity(),
    httpStartedTransactions: 0,
    httpRetryStartedTransactions: 0,
    httpCompletedTransactions: 0,
    httpSucceededTransactions: 0,
    httpRejectedTransactions: 0,
    terminalFailedTransactions: 0,
  }
}

function createWorker(ordinal: number): SenderWorker {
  return {
    id: `sender-worker-${ordinal}`,
    ordinal,
    retiring: false,
    state: 'idle',
    batch: null,
    attempt: 0,
    completeAtMs: 0,
    retryAtMs: 0,
    outcome: null,
    attemptLatencyMs: 0,
    hadAmbiguousOutcome: false,
  }
}

function roundedStepDuration(durationMs: number): number {
  const finiteDuration = Number.isFinite(durationMs) ? durationMs : FIXED_STEP_MS
  return Math.ceil(Math.max(0, finiteDuration) / FIXED_STEP_MS) * FIXED_STEP_MS
}

export function deterministicRetryDelayMs(
  batchSequence: number,
  retryNumber: 1 | 2,
): number {
  const nominal = RETRY_BACKOFF_BASE_MS *
    RETRY_BACKOFF_MULTIPLIER ** (retryNumber - 1)
  const factorIndex = ((batchSequence + retryNumber) %
    RETRY_JITTER_FACTORS.length + RETRY_JITTER_FACTORS.length) %
    RETRY_JITTER_FACTORS.length
  return roundedStepDuration(nominal * RETRY_JITTER_FACTORS[factorIndex])
}

class StatefulQueue<TItem> {
  private readonly items: TItem[] = []
  private readonly transactionsOf: (item: TItem) => number
  private appliedCapacity: number
  private previewCapacity: number | null = null
  private pendingCapacity: number | null = null
  private blockedForMs = 0

  enqueuedBatchesTotal = 0
  enqueuedTransactionsTotal = 0
  dequeuedBatchesTotal = 0
  dequeuedTransactionsTotal = 0
  handoffBatchesTotal = 0
  blockedMsTotal = 0
  blockedSenders = 0

  constructor(
    capacity: number,
    transactionsOf: (item: TItem) => number,
  ) {
    this.appliedCapacity = capacity
    this.transactionsOf = transactionsOf
  }

  get depthBatches(): number {
    return this.items.length
  }

  get queuedTransactions(): number {
    return this.items.reduce(
      (total, item) => total + this.transactionsOf(item),
      0,
    )
  }

  get capacity(): CapacityTelemetry {
    return {
      applied: this.appliedCapacity,
      preview: this.previewCapacity,
      pending: this.pendingCapacity,
    }
  }

  get oldestBlockedSenderMs(): number {
    return this.blockedForMs
  }

  requestCapacity(capacity: number): boolean {
    if (
      capacity === this.appliedCapacity &&
      this.previewCapacity === null &&
      this.pendingCapacity === null
    ) {
      return false
    }

    if (capacity >= this.depthBatches) {
      this.appliedCapacity = capacity
      this.previewCapacity = null
      this.pendingCapacity = null
      return true
    }

    this.previewCapacity = capacity
    this.pendingCapacity = capacity
    return true
  }

  peek(): TItem | null {
    return this.items[0] ?? null
  }

  enqueue(item: TItem, activity: QueueActivity): boolean {
    if (this.appliedCapacity === 0 || this.depthBatches >= this.appliedCapacity) {
      return false
    }
    this.items.push(item)
    this.recordInput(item, activity)
    return true
  }

  dequeue(activity: QueueActivity): TItem | null {
    const item = this.items.shift()
    if (item === undefined) return null
    this.recordOutput(item, activity)
    this.applyPendingCapacity()
    return item
  }

  handoff(item: TItem, activity: QueueActivity): void {
    this.recordInput(item, activity)
    this.recordOutput(item, activity)
    this.handoffBatchesTotal += 1
    activity.handoffBatches += 1
  }

  observeBlocked(blocked: boolean, senderProgressed: boolean): void {
    this.blockedSenders = blocked ? 1 : 0
    if (blocked) {
      this.blockedForMs = senderProgressed
        ? FIXED_STEP_MS
        : this.blockedForMs + FIXED_STEP_MS
      this.blockedMsTotal += FIXED_STEP_MS
    } else {
      this.blockedForMs = 0
    }
  }

  telemetry(
    activity: QueueActivity,
    rateSeconds: number,
    running: boolean,
  ): QueueTelemetry {
    const inputBatchesPerSecond =
      rateSeconds === 0 ? 0 : activity.inputBatches / rateSeconds
    const outputBatchesPerSecond =
      rateSeconds === 0 ? 0 : activity.outputBatches / rateSeconds
    const inputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.inputTransactions / rateSeconds
    const outputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.outputTransactions / rateSeconds
    const trend: QueueTrend = !running
      ? 'steady'
      : inputTransactionsPerSecond > outputTransactionsPerSecond
        ? 'rising'
        : inputTransactionsPerSecond < outputTransactionsPerSecond
          ? 'falling'
          : 'steady'
    return {
      capacity: this.capacity,
      enqueuedBatchesTotal: this.enqueuedBatchesTotal,
      enqueuedTransactionsTotal: this.enqueuedTransactionsTotal,
      dequeuedBatchesTotal: this.dequeuedBatchesTotal,
      dequeuedTransactionsTotal: this.dequeuedTransactionsTotal,
      depthBatches: this.depthBatches,
      queuedTransactions: this.queuedTransactions,
      handoffBatches: running ? activity.handoffBatches : 0,
      handoffBatchesTotal: this.handoffBatchesTotal,
      blockedSenders: running ? this.blockedSenders : 0,
      oldestBlockedSenderMs: running ? this.oldestBlockedSenderMs : 0,
      blockedMsTotal: this.blockedMsTotal,
      inputBatchesPerSecond: running ? inputBatchesPerSecond : 0,
      outputBatchesPerSecond: running ? outputBatchesPerSecond : 0,
      inputTransactionsPerSecond: running ? inputTransactionsPerSecond : 0,
      outputTransactionsPerSecond: running ? outputTransactionsPerSecond : 0,
      trend,
    }
  }

  resetRuntime(): void {
    this.items.length = 0
    if (this.pendingCapacity !== null) {
      this.appliedCapacity = this.pendingCapacity
    }
    this.previewCapacity = null
    this.pendingCapacity = null
    this.blockedForMs = 0
    this.enqueuedBatchesTotal = 0
    this.enqueuedTransactionsTotal = 0
    this.dequeuedBatchesTotal = 0
    this.dequeuedTransactionsTotal = 0
    this.handoffBatchesTotal = 0
    this.blockedMsTotal = 0
    this.blockedSenders = 0
  }

  private recordInput(item: TItem, activity: QueueActivity): void {
    const transactions = this.transactionsOf(item)
    this.enqueuedBatchesTotal += 1
    this.enqueuedTransactionsTotal += transactions
    activity.inputBatches += 1
    activity.inputTransactions += transactions
  }

  private recordOutput(item: TItem, activity: QueueActivity): void {
    const transactions = this.transactionsOf(item)
    this.dequeuedBatchesTotal += 1
    this.dequeuedTransactionsTotal += transactions
    activity.outputBatches += 1
    activity.outputTransactions += transactions
  }

  private applyPendingCapacity(): void {
    if (
      this.pendingCapacity !== null &&
      this.depthBatches <= this.pendingCapacity
    ) {
      this.appliedCapacity = this.pendingCapacity
      this.previewCapacity = null
      this.pendingCapacity = null
    }
  }
}

export class FixedStepSimulation {
  readonly config: SimulationConfig

  private readonly queue1: StatefulQueue<number>
  private readonly queue2: StatefulQueue<SimulationBatch>
  private readonly activities: StepActivity[] = []
  private readonly attemptOutcomeSource:
    | SimulationAttemptOutcomeSource
    | undefined
  private workers: SenderWorker[]
  private nextWorkerIndex = 0
  private readerTransactionCredit = 0
  private throttlerTokens = 0
  private throttlerBufferedTransactions = 0
  private pendingHttpBatch: SimulationBatch | null = null
  private nextBatchSequence = 0
  private failureCredit = 0
  private latestStatusCode: number | null = null
  private lastOutcome: HttpLastOutcome = null
  private latestLatencyMs: number | null = null
  private latestResponseLatencyMs: number | null = null
  private requestStartedTotal = 0
  private retryAttemptStartedTotal = 0
  private requestCompletedTotal = 0
  private requestSucceededTotal = 0
  private requestFailedTotal = 0
  private responseRejectedTotal = 0
  private requestTimedOutTotal = 0
  private networkErrorTotal = 0
  private http503ResponseTotal = 0
  private successfulTransactionsTotal = 0
  private failedAttemptTransactionsTotal = 0
  private terminalFailedBatchesTotal = 0
  private terminalFailedTransactionsTotal = 0
  private ambiguousTimeoutTransactionsTotal = 0
  private duplicateRiskTransactionsTotal = 0
  private ambiguousTerminalTransactionsTotal = 0
  private elapsedMs = 0
  private limitedMs = 0

  constructor(
    config: SimulationConfig,
    queue1Capacity: number,
    queue2Capacity: number,
    attemptOutcomeSource?: SimulationAttemptOutcomeSource,
  ) {
    this.config = { ...config }
    this.queue1 = new StatefulQueue(queue1Capacity, (transactions) => transactions)
    this.queue2 = new StatefulQueue(
      queue2Capacity,
      (batch) => batch.transactions,
    )
    this.attemptOutcomeSource = attemptOutcomeSource
    this.workers = Array.from(
      { length: config.senderWorkers },
      (_, ordinal) => createWorker(ordinal),
    )
    this.nextWorkerIndex = 0
  }

  advanceStep(): void {
    this.elapsedMs += FIXED_STEP_MS
    const activity = createStepActivity()

    this.completeDueAttempts(activity)
    this.startDueRetries(activity)
    this.finalizeSenderScaleDown()
    this.refillThrottlerTokens()
    this.drainQueue2(activity)
    const queue2Blocked = this.flushThrottlerBuffer(activity)
    this.receiveQueue1(activity)
    const queue1Blocked = this.produceReaderBatches(activity)

    this.queue1.observeBlocked(
      queue1Blocked,
      activity.queue1.inputBatches > 0 || activity.queue1.handoffBatches > 0,
    )
    this.queue2.observeBlocked(
      queue2Blocked,
      activity.queue2.inputBatches > 0 || activity.queue2.handoffBatches > 0,
    )
    if (
      this.config.requestedTps > this.readerCapacityTps ||
      queue1Blocked ||
      queue2Blocked
    ) {
      this.limitedMs += FIXED_STEP_MS
    }

    this.activities.push(activity)
    if (this.activities.length > RATE_WINDOW_STEPS) this.activities.shift()
  }

  updateConfig(values: Partial<SimulationConfig>): void {
    const senderWorkers = values.senderWorkers
    Object.assign(this.config, values)
    if (senderWorkers !== undefined) {
      this.requestSenderWorkerCount(senderWorkers)
    }
    if (
      values.requestedTps === 0 ||
      values.throttlerInstallationMode !== undefined
    ) {
      this.throttlerTokens = 0
    }
    this.readerTransactionCredit = Math.min(
      this.readerTransactionCredit,
      this.config.readerWorkers * this.config.readBatchSize,
    )
  }

  requestQueueCapacity(queue: 1 | 2, capacity: number): boolean {
    return (queue === 1 ? this.queue1 : this.queue2).requestCapacity(capacity)
  }

  clearInstantaneousTelemetry(): void {
    this.activities.length = 0
  }

  reset(): void {
    this.queue1.resetRuntime()
    this.queue2.resetRuntime()
    this.activities.length = 0
    this.workers = Array.from(
      { length: this.config.senderWorkers },
      (_, ordinal) => createWorker(ordinal),
    )
    this.nextWorkerIndex = 0
    this.readerTransactionCredit = 0
    this.throttlerTokens = 0
    this.throttlerBufferedTransactions = 0
    this.pendingHttpBatch = null
    this.nextBatchSequence = 0
    this.failureCredit = 0
    this.latestStatusCode = null
    this.lastOutcome = null
    this.latestLatencyMs = null
    this.latestResponseLatencyMs = null
    this.requestStartedTotal = 0
    this.retryAttemptStartedTotal = 0
    this.requestCompletedTotal = 0
    this.requestSucceededTotal = 0
    this.requestFailedTotal = 0
    this.responseRejectedTotal = 0
    this.requestTimedOutTotal = 0
    this.networkErrorTotal = 0
    this.http503ResponseTotal = 0
    this.successfulTransactionsTotal = 0
    this.failedAttemptTransactionsTotal = 0
    this.terminalFailedBatchesTotal = 0
    this.terminalFailedTransactionsTotal = 0
    this.ambiguousTimeoutTransactionsTotal = 0
    this.duplicateRiskTransactionsTotal = 0
    this.ambiguousTerminalTransactionsTotal = 0
    this.elapsedMs = 0
    this.limitedMs = 0
  }

  telemetry(running: boolean): SimulationTelemetry {
    const aggregate = this.aggregateActivity()
    const rateSeconds = RATE_WINDOW_STEPS * FIXED_STEP_MS / 1_000
    const queue1 = this.queue1.telemetry(aggregate.queue1, rateSeconds, running)
    const queue2 = this.queue2.telemetry(aggregate.queue2, rateSeconds, running)
    const divisor = rateSeconds === 0 ? 1 : rateSeconds
    const workerSlots = this.workerSlots
    const workerStates = this.workerStateCounts(workerSlots)

    return {
      elapsedMs: this.elapsedMs,
      totalTransactions: this.queue1.dequeuedTransactionsTotal,
      limitedMs: this.limitedMs,
      readerCapacityTps: this.readerCapacityTps,
      readerTransactionsPerSecond: queue1.inputTransactionsPerSecond,
      admittedTransactionsPerSecond: queue1.outputTransactionsPerSecond,
      attemptedTransactionsPerSecond: running
        ? aggregate.httpStartedTransactions / divisor
        : 0,
      acceptedTransactionsPerSecond: running
        ? aggregate.httpSucceededTransactions / divisor
        : 0,
      rejectedTransactionsPerSecond: running
        ? aggregate.httpRejectedTransactions / divisor
        : 0,
      queue1,
      queue2,
      sender: {
        workers: {
          applied: this.workers.length,
          preview: null,
          pending: this.workers.length === this.config.senderWorkers
            ? null
            : this.config.senderWorkers,
        },
        workerStates,
        workerSlots,
        retryAttemptsStartedTotal: this.retryAttemptStartedTotal,
        retryAttemptedTransactionsPerSecond: running
          ? aggregate.httpRetryStartedTransactions / divisor
          : 0,
        terminalFailedTransactionsPerSecond: running
          ? aggregate.terminalFailedTransactions / divisor
          : 0,
        terminalFailedBatchesTotal: this.terminalFailedBatchesTotal,
        terminalFailedTransactionsTotal: this.terminalFailedTransactionsTotal,
        ambiguousTimeoutTransactionsTotal:
          this.ambiguousTimeoutTransactionsTotal,
        duplicateRiskTransactionsTotal: this.duplicateRiskTransactionsTotal,
        ambiguousTerminalTransactionsTotal:
          this.ambiguousTerminalTransactionsTotal,
      },
      http: {
        requestsStartedTotal: this.requestStartedTotal,
        requestsCompletedTotal: this.requestCompletedTotal,
        requestsSucceededTotal: this.requestSucceededTotal,
        requestsFailedTotal: this.requestFailedTotal,
        responsesRejectedTotal: this.responseRejectedTotal,
        requestsTimedOutTotal: this.requestTimedOutTotal,
        networkErrorsTotal: this.networkErrorTotal,
        successfulTransactionsTotal: this.successfulTransactionsTotal,
        failedAttemptTransactionsTotal: this.failedAttemptTransactionsTotal,
        inFlightRequests: workerStates.inFlight,
        startedTransactionsPerSecond: running
          ? aggregate.httpStartedTransactions / divisor
          : 0,
        completedTransactionsPerSecond: running
          ? aggregate.httpCompletedTransactions / divisor
          : 0,
        succeededTransactionsPerSecond: running
          ? aggregate.httpSucceededTransactions / divisor
          : 0,
        rejectedTransactionsPerSecond: running
          ? aggregate.httpRejectedTransactions / divisor
          : 0,
        latestStatusCode: this.latestStatusCode,
        lastOutcome: this.lastOutcome,
        latestLatencyMs: this.latestLatencyMs,
        latestResponseLatencyMs: this.latestResponseLatencyMs,
      },
    }
  }

  get http503ResponsesTotal(): number {
    return this.http503ResponseTotal
  }

  private get readerCapacityTps(): number {
    return this.config.readerWorkers * READER_TRANSACTIONS_PER_WORKER_SECOND
  }

  private get workerSlots(): readonly SenderWorkerSlotTelemetry[] {
    return this.workers.map(({ id, ordinal, state }) => ({
      id,
      ordinal,
      state,
    }))
  }

  private workerStateCounts(
    workerSlots: readonly SenderWorkerSlotTelemetry[],
  ): SenderWorkerStateCounts {
    let inFlight = 0
    let backoff = 0
    for (const slot of workerSlots) {
      if (slot.state === 'in-flight') inFlight += 1
      if (slot.state === 'backoff') backoff += 1
    }
    return {
      idle: workerSlots.length - inFlight - backoff,
      inFlight,
      backoff,
    }
  }

  private completeDueAttempts(activity: StepActivity): void {
    for (const worker of this.workers) {
      if (
        worker.state !== 'in-flight' ||
        worker.completeAtMs > this.elapsedMs
      ) {
        continue
      }
      this.completeAttempt(worker, activity)
    }
  }

  private completeAttempt(
    worker: SenderWorker,
    activity: StepActivity,
  ): void {
    const batch = worker.batch
    const outcome = worker.outcome
    if (batch === null || outcome === null) {
      throw new Error('in-flight worker must own a batch and outcome')
    }

    this.requestCompletedTotal += 1
    activity.httpCompletedTransactions += batch.transactions
    this.latestLatencyMs = worker.attemptLatencyMs

    if (outcome.kind === 'http-response') {
      this.lastOutcome = 'http-response'
      this.latestStatusCode = outcome.statusCode
      this.latestResponseLatencyMs = worker.attemptLatencyMs
      if (outcome.statusCode >= 200 && outcome.statusCode < 300) {
        this.requestSucceededTotal += 1
        this.successfulTransactionsTotal += batch.transactions
        activity.httpSucceededTransactions += batch.transactions
        this.releaseWorker(worker)
        return
      }

      this.requestFailedTotal += 1
      this.responseRejectedTotal += 1
      this.failedAttemptTransactionsTotal += batch.transactions
      activity.httpRejectedTransactions += batch.transactions
      if (outcome.statusCode === 503) this.http503ResponseTotal += 1
      if (
        RETRYABLE_STATUS_CODES.includes(outcome.statusCode) &&
        worker.attempt < RETRY_MAX_ATTEMPTS
      ) {
        this.scheduleRetry(worker)
        return
      }
      this.failTerminally(worker, activity)
      return
    }

    this.requestFailedTotal += 1
    this.failedAttemptTransactionsTotal += batch.transactions
    this.latestStatusCode = null
    worker.hadAmbiguousOutcome = true
    if (outcome.kind === 'timeout') {
      this.lastOutcome = 'timeout'
      this.requestTimedOutTotal += 1
      this.ambiguousTimeoutTransactionsTotal += batch.transactions
    } else {
      this.lastOutcome = 'network-error'
      this.networkErrorTotal += 1
    }

    if (worker.attempt < RETRY_MAX_ATTEMPTS) {
      this.scheduleRetry(worker)
      return
    }
    this.failTerminally(worker, activity)
  }

  private scheduleRetry(worker: SenderWorker): void {
    const batch = worker.batch
    if (batch === null || worker.attempt >= RETRY_MAX_ATTEMPTS) {
      throw new Error('retry requires an owned batch and remaining attempt')
    }
    const retryNumber = worker.attempt as 1 | 2
    worker.state = 'backoff'
    worker.retryAtMs =
      this.elapsedMs + deterministicRetryDelayMs(batch.sequence, retryNumber)
    worker.completeAtMs = 0
    worker.outcome = null
    worker.attemptLatencyMs = 0
  }

  private failTerminally(
    worker: SenderWorker,
    activity: StepActivity,
  ): void {
    const batch = worker.batch
    if (batch === null) throw new Error('terminal failure requires owned batch')
    this.terminalFailedBatchesTotal += 1
    this.terminalFailedTransactionsTotal += batch.transactions
    activity.terminalFailedTransactions += batch.transactions
    if (worker.hadAmbiguousOutcome) {
      this.ambiguousTerminalTransactionsTotal += batch.transactions
    }
    this.releaseWorker(worker)
  }

  private releaseWorker(worker: SenderWorker): void {
    worker.state = 'idle'
    worker.batch = null
    worker.attempt = 0
    worker.completeAtMs = 0
    worker.retryAtMs = 0
    worker.outcome = null
    worker.attemptLatencyMs = 0
    worker.hadAmbiguousOutcome = false
  }

  private startDueRetries(activity: StepActivity): void {
    for (const worker of this.workers) {
      if (
        worker.state !== 'backoff' ||
        worker.retryAtMs > this.elapsedMs
      ) {
        continue
      }
      this.startAttempt(worker, worker.attempt + 1, activity)
    }
  }

  private refillThrottlerTokens(): void {
    if (this.config.throttlerInstallationMode === 'bypass') {
      this.throttlerTokens = 0
      return
    }
    const headTransactions = this.queue1.peek() ?? this.config.readBatchSize
    const tokenCapacity = Math.max(headTransactions, this.config.readBatchSize)
    this.throttlerTokens = Math.min(
      tokenCapacity,
      this.throttlerTokens + this.config.requestedTps * FIXED_STEP_MS / 1_000,
    )
  }

  private drainQueue2(activity: StepActivity): void {
    while (this.queue2.peek() !== null) {
      const worker = this.nextIdleWorker()
      if (worker === null) return
      const batch = this.queue2.dequeue(activity.queue2)
      if (batch === null) return
      worker.batch = batch
      worker.hadAmbiguousOutcome = false
      this.startAttempt(worker, 1, activity)
    }
  }

  private flushThrottlerBuffer(activity: StepActivity): boolean {
    let blocked = false
    while (
      this.pendingHttpBatch !== null ||
      this.throttlerBufferedTransactions >= this.config.httpBatchSize
    ) {
      const batch = this.pendingHttpBatch ?? this.createHttpBatch()
      this.pendingHttpBatch = batch
      if (this.queue2.capacity.applied === 0) {
        const worker = this.nextIdleWorker()
        if (worker === null) {
          blocked = true
          break
        }
        this.queue2.handoff(batch, activity.queue2)
        worker.batch = batch
        worker.hadAmbiguousOutcome = false
        this.startAttempt(worker, 1, activity)
      } else if (!this.queue2.enqueue(batch, activity.queue2)) {
        blocked = true
        break
      }
      this.throttlerBufferedTransactions -= batch.transactions
      this.pendingHttpBatch = null
    }
    return blocked
  }

  private createHttpBatch(): SimulationBatch {
    const sequence = this.nextBatchSequence
    this.nextBatchSequence += 1
    return {
      sequence,
      identity: 'http-batch-' + sequence,
      transactions: this.config.httpBatchSize,
    }
  }

  private receiveQueue1(activity: StepActivity): void {
    if (
      this.pendingHttpBatch !== null ||
      this.throttlerBufferedTransactions >= this.config.httpBatchSize
    ) {
      return
    }
    const transactions = this.queue1.peek()
    if (transactions === null) return
    const installed = this.config.throttlerInstallationMode === 'installed'
    if (installed && this.throttlerTokens < transactions) return
    const received = this.queue1.dequeue(activity.queue1)
    if (received === null) return
    if (installed) this.throttlerTokens -= received
    this.throttlerBufferedTransactions += received
  }

  private produceReaderBatches(activity: StepActivity): boolean {
    const transactions = this.config.readBatchSize
    this.readerTransactionCredit = Math.min(
      this.config.readerWorkers * transactions,
      this.readerTransactionCredit +
        this.readerCapacityTps * FIXED_STEP_MS / 1_000,
    )
    if (this.readerTransactionCredit < transactions) return false

    while (this.readerTransactionCredit >= transactions) {
      if (this.queue1.capacity.applied === 0) {
        if (
          this.throttlerBufferedTransactions !== 0 ||
          (
            this.config.throttlerInstallationMode === 'installed' &&
            this.throttlerTokens < transactions
          )
        ) {
          return true
        }
        this.queue1.handoff(transactions, activity.queue1)
        if (this.config.throttlerInstallationMode === 'installed') {
          this.throttlerTokens -= transactions
        }
        this.throttlerBufferedTransactions += transactions
      } else if (!this.queue1.enqueue(transactions, activity.queue1)) {
        return true
      }

      this.readerTransactionCredit -= transactions
    }

    return false
  }

  private startAttempt(
    worker: SenderWorker,
    attempt: number,
    activity: StepActivity,
  ): void {
    const batch = worker.batch
    if (batch === null) throw new Error('attempt requires an owned batch')
    const context: SimulationAttemptContext = {
      batch,
      attempt,
      startedAtMs: this.elapsedMs,
      targetDelayMs: this.config.targetDelayMs,
      targetErrorRatePercent: this.config.targetErrorRatePercent,
      httpTimeoutMs: this.config.httpTimeoutMs,
    }
    const selected = this.attemptOutcomeSource?.(context) ??
      this.defaultAttemptOutcome(context)
    const latencyMs = roundedStepDuration(selected.latencyMs)
    const outcome = selected.kind === 'http-response'
      ? { ...selected, latencyMs }
      : { ...selected, latencyMs }

    worker.state = 'in-flight'
    worker.attempt = attempt
    worker.completeAtMs = this.elapsedMs + latencyMs
    worker.retryAtMs = 0
    worker.outcome = outcome
    worker.attemptLatencyMs = latencyMs
    this.requestStartedTotal += 1
    activity.httpStartedTransactions += batch.transactions
    if (attempt > 1) {
      this.retryAttemptStartedTotal += 1
      activity.httpRetryStartedTransactions += batch.transactions
      if (worker.hadAmbiguousOutcome) {
        this.duplicateRiskTransactionsTotal += batch.transactions
      }
    }
  }

  private defaultAttemptOutcome(
    context: SimulationAttemptContext,
  ): SimulationAttemptOutcome {
    const targetLatencyMs = context.targetDelayMs + 5
    if (targetLatencyMs > context.httpTimeoutMs) {
      return {
        kind: 'timeout',
        latencyMs: context.httpTimeoutMs,
      }
    }
    return {
      kind: 'http-response',
      statusCode: this.nextRequestFails() ? 503 : 200,
      latencyMs: targetLatencyMs,
    }
  }

  private nextRequestFails(): boolean {
    this.failureCredit += this.config.targetErrorRatePercent
    if (this.failureCredit < 100) return false
    this.failureCredit -= 100
    return true
  }

  private nextIdleWorker(): SenderWorker | null {
    for (let offset = 0; offset < this.workers.length; offset += 1) {
      const index = (this.nextWorkerIndex + offset) % this.workers.length
      const worker = this.workers[index]
      if (worker.retiring || worker.state !== 'idle') continue
      this.nextWorkerIndex = (index + 1) % this.workers.length
      return worker
    }
    return null
  }

  private requestSenderWorkerCount(requested: number): void {
    this.workers.forEach((worker) => {
      worker.retiring = false
    })
    if (requested > this.workers.length) {
      const firstOrdinal = this.workers.length
      for (let ordinal = firstOrdinal; ordinal < requested; ordinal += 1) {
        this.workers.push(createWorker(ordinal))
      }
      return
    }
    if (requested === this.workers.length) return

    const retireCount = this.workers.length - requested
    for (
      let index = this.workers.length - retireCount;
      index < this.workers.length;
      index += 1
    ) {
      this.workers[index].retiring = true
    }
    this.finalizeSenderScaleDown()
  }

  private finalizeSenderScaleDown(): void {
    const retiring = this.workers.filter((worker) => worker.retiring)
    if (
      retiring.length === 0 ||
      retiring.some((worker) => worker.state !== 'idle')
    ) {
      return
    }
    this.workers = this.workers.filter((worker) => !worker.retiring)
    this.nextWorkerIndex %= this.workers.length
  }

  private aggregateActivity(): StepActivity {
    const aggregate = createStepActivity()
    for (const activity of this.activities) {
      aggregate.queue1.inputBatches += activity.queue1.inputBatches
      aggregate.queue1.inputTransactions += activity.queue1.inputTransactions
      aggregate.queue1.outputBatches += activity.queue1.outputBatches
      aggregate.queue1.outputTransactions += activity.queue1.outputTransactions
      aggregate.queue1.handoffBatches += activity.queue1.handoffBatches
      aggregate.queue2.inputBatches += activity.queue2.inputBatches
      aggregate.queue2.inputTransactions += activity.queue2.inputTransactions
      aggregate.queue2.outputBatches += activity.queue2.outputBatches
      aggregate.queue2.outputTransactions += activity.queue2.outputTransactions
      aggregate.queue2.handoffBatches += activity.queue2.handoffBatches
      aggregate.httpStartedTransactions += activity.httpStartedTransactions
      aggregate.httpRetryStartedTransactions +=
        activity.httpRetryStartedTransactions
      aggregate.httpCompletedTransactions += activity.httpCompletedTransactions
      aggregate.httpSucceededTransactions += activity.httpSucceededTransactions
      aggregate.httpRejectedTransactions += activity.httpRejectedTransactions
      aggregate.terminalFailedTransactions +=
        activity.terminalFailedTransactions
    }
    return aggregate
  }
}
