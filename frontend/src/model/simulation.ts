import type {
  HttpLastOutcome,
  ChannelTrend,
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
  httpTimeoutMs: number
  targetDelayMs: number
  targetErrorRatePercent: number
}

export interface CapacityTelemetry {
  readonly applied: number
  readonly preview: number | null
  readonly pending: number | null
}

export interface ChannelTelemetry {
  readonly capacity: CapacityTelemetry
  readonly sentBatchesTotal: number
  readonly sentTransactionsTotal: number
  readonly receivedBatchesTotal: number
  readonly receivedTransactionsTotal: number
  readonly depthBatches: number
  readonly bufferedTransactions: number
  readonly handoffBatches: number
  readonly handoffBatchesTotal: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly blockedMsTotal: number
  readonly inputBatchesPerSecond: number
  readonly outputBatchesPerSecond: number
  readonly inputTransactionsPerSecond: number
  readonly outputTransactionsPerSecond: number
  readonly trend: ChannelTrend
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
  readonly workerStates: WorkerStateCounts
  readonly drainingWorkerStates: WorkerStateCounts
  readonly retryAttemptsStartedTotal: number
  readonly retryAttemptedTransactionsPerSecond: number
  readonly terminalFailedTransactionsPerSecond: number
  readonly terminalFailedBatchesTotal: number
  readonly terminalFailedTransactionsTotal: number
  readonly ambiguousTimeoutTransactionsTotal: number
  readonly duplicateRiskTransactionsTotal: number
  readonly ambiguousTerminalTransactionsTotal: number
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
  readonly readerChannel: ChannelTelemetry
  readonly senderChannel: ChannelTelemetry
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

interface ChannelActivity {
  inputBatches: number
  inputTransactions: number
  outputBatches: number
  outputTransactions: number
  handoffBatches: number
}

interface StepActivity {
  readerChannel: ChannelActivity
  senderChannel: ChannelActivity
  httpStartedTransactions: number
  httpRetryStartedTransactions: number
  httpCompletedTransactions: number
  httpSucceededTransactions: number
  httpRejectedTransactions: number
  terminalFailedTransactions: number
}

interface AdmissionResult {
  senderChannelBlocked: boolean
  tokenLimited: boolean
}

interface ReaderProductionResult extends AdmissionResult {
  readerChannelBlocked: boolean
}

type SenderWorkerState = 'idle' | 'in-flight' | 'backoff'

interface WorkerStateCounts {
  readonly idle: number
  readonly inFlight: number
  readonly backoff: number
}

interface SenderWorker {
  readonly workerId: number
  retiring: boolean
  terminalError: boolean
  state: SenderWorkerState
  batch: SimulationBatch | null
  attempt: number
  completeAtMs: number
  retryAtMs: number
  outcome: SimulationAttemptOutcome | null
  attemptLatencyMs: number
  hadAmbiguousOutcome: boolean
}

function createChannelActivity(): ChannelActivity {
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
    readerChannel: createChannelActivity(),
    senderChannel: createChannelActivity(),
    httpStartedTransactions: 0,
    httpRetryStartedTransactions: 0,
    httpCompletedTransactions: 0,
    httpSucceededTransactions: 0,
    httpRejectedTransactions: 0,
    terminalFailedTransactions: 0,
  }
}

function createWorker(workerId: number): SenderWorker {
  return {
    workerId,
    retiring: false,
    terminalError: false,
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

class StatefulChannel<TItem> {
  private readonly items: TItem[] = []
  private readonly transactionsOf: (item: TItem) => number
  private appliedCapacity: number
  private previewCapacity: number | null = null
  private pendingCapacity: number | null = null
  private blockedForMs = 0

  sentBatchesTotal = 0
  sentTransactionsTotal = 0
  receivedBatchesTotal = 0
  receivedTransactionsTotal = 0
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

  get bufferedTransactions(): number {
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

  send(item: TItem, activity: ChannelActivity): boolean {
    if (this.appliedCapacity === 0 || this.depthBatches >= this.appliedCapacity) {
      return false
    }
    this.items.push(item)
    this.recordInput(item, activity)
    return true
  }

  receive(activity: ChannelActivity): TItem | null {
    const item = this.items.shift()
    if (item === undefined) return null
    this.recordOutput(item, activity)
    this.applyPendingCapacity()
    return item
  }

  handoff(item: TItem, activity: ChannelActivity): void {
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
    activity: ChannelActivity,
    rateSeconds: number,
    running: boolean,
  ): ChannelTelemetry {
    const inputBatchesPerSecond =
      rateSeconds === 0 ? 0 : activity.inputBatches / rateSeconds
    const outputBatchesPerSecond =
      rateSeconds === 0 ? 0 : activity.outputBatches / rateSeconds
    const inputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.inputTransactions / rateSeconds
    const outputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.outputTransactions / rateSeconds
    const trend: ChannelTrend = !running
      ? 'steady'
      : inputTransactionsPerSecond > outputTransactionsPerSecond
        ? 'rising'
        : inputTransactionsPerSecond < outputTransactionsPerSecond
          ? 'falling'
          : 'steady'
    return {
      capacity: this.capacity,
      sentBatchesTotal: this.sentBatchesTotal,
      sentTransactionsTotal: this.sentTransactionsTotal,
      receivedBatchesTotal: this.receivedBatchesTotal,
      receivedTransactionsTotal: this.receivedTransactionsTotal,
      depthBatches: this.depthBatches,
      bufferedTransactions: this.bufferedTransactions,
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
    this.sentBatchesTotal = 0
    this.sentTransactionsTotal = 0
    this.receivedBatchesTotal = 0
    this.receivedTransactionsTotal = 0
    this.handoffBatchesTotal = 0
    this.blockedMsTotal = 0
    this.blockedSenders = 0
  }

  private recordInput(item: TItem, activity: ChannelActivity): void {
    const transactions = this.transactionsOf(item)
    this.sentBatchesTotal += 1
    this.sentTransactionsTotal += transactions
    activity.inputBatches += 1
    activity.inputTransactions += transactions
  }

  private recordOutput(item: TItem, activity: ChannelActivity): void {
    const transactions = this.transactionsOf(item)
    this.receivedBatchesTotal += 1
    this.receivedTransactionsTotal += transactions
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

  private readonly readerChannel: StatefulChannel<SimulationBatch>
  private readonly senderChannel: StatefulChannel<SimulationBatch>
  private readonly activities: StepActivity[] = []
  private readonly attemptOutcomeSource:
    | SimulationAttemptOutcomeSource
    | undefined
  private workers: SenderWorker[]
  private nextWorkerIndex = 0
  private readerTransactionCredit = 0
  private throttlerTokens = 0
  private pendingThrottledBatch: SimulationBatch | null = null
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
    readerChannelCapacity: number,
    senderChannelCapacity: number,
    attemptOutcomeSource?: SimulationAttemptOutcomeSource,
  ) {
    this.config = { ...config }
    this.readerChannel = new StatefulChannel(
      readerChannelCapacity,
      (batch) => batch.transactions,
    )
    this.senderChannel = new StatefulChannel(
      senderChannelCapacity,
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
    this.drainSenderChannel(activity)
    let senderChannelBlocked = this.flushThrottledBatch(activity)
    const admission = this.receiveReaderChannel(activity)
    senderChannelBlocked ||= admission.senderChannelBlocked
    const production = this.produceReaderBatches(activity)
    senderChannelBlocked ||= production.senderChannelBlocked

    this.readerChannel.observeBlocked(
      production.readerChannelBlocked,
      activity.readerChannel.inputBatches > 0 || activity.readerChannel.handoffBatches > 0,
    )
    this.senderChannel.observeBlocked(
      senderChannelBlocked,
      activity.senderChannel.inputBatches > 0 || activity.senderChannel.handoffBatches > 0,
    )
    if (admission.tokenLimited || production.tokenLimited) {
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

  requestChannelCapacity(channel: 1 | 2, capacity: number): boolean {
    return (channel === 1 ? this.readerChannel : this.senderChannel).requestCapacity(capacity)
  }

  clearInstantaneousTelemetry(): void {
    this.activities.length = 0
  }

  reset(): void {
    this.readerChannel.resetRuntime()
    this.senderChannel.resetRuntime()
    this.activities.length = 0
    this.workers = Array.from(
      { length: this.config.senderWorkers },
      (_, ordinal) => createWorker(ordinal),
    )
    this.nextWorkerIndex = 0
    this.readerTransactionCredit = 0
    this.throttlerTokens = 0
    this.pendingThrottledBatch = null
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
    const readerChannel = this.readerChannel.telemetry(aggregate.readerChannel, rateSeconds, running)
    const senderChannel = this.senderChannel.telemetry(aggregate.senderChannel, rateSeconds, running)
    const divisor = rateSeconds === 0 ? 1 : rateSeconds
    const { active: workerStates, draining: drainingWorkerStates } =
      this.workerStateCounts()

    return {
      elapsedMs: this.elapsedMs,
      totalTransactions: this.readerChannel.receivedTransactionsTotal,
      limitedMs: this.limitedMs,
      readerCapacityTps: this.readerCapacityTps,
      readerTransactionsPerSecond: readerChannel.inputTransactionsPerSecond,
      admittedTransactionsPerSecond: readerChannel.outputTransactionsPerSecond,
      attemptedTransactionsPerSecond: running
        ? aggregate.httpStartedTransactions / divisor
        : 0,
      acceptedTransactionsPerSecond: running
        ? aggregate.httpSucceededTransactions / divisor
        : 0,
      rejectedTransactionsPerSecond: running
        ? aggregate.httpRejectedTransactions / divisor
        : 0,
      readerChannel,
      senderChannel,
      sender: {
        workers: {
          applied: this.workers.length,
          preview: null,
          pending: this.workers.length === this.config.senderWorkers
            ? null
            : this.config.senderWorkers,
        },
        workerStates,
        drainingWorkerStates,
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
        inFlightRequests:
          workerStates.inFlight + drainingWorkerStates.inFlight,
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

  private workerStateCounts(): {
    readonly active: WorkerStateCounts
    readonly draining: WorkerStateCounts
  } {
    const active = { idle: 0, inFlight: 0, backoff: 0 }
    const draining = { idle: 0, inFlight: 0, backoff: 0 }

    for (const worker of this.workers) {
      const counts = worker.retiring ? draining : active
      switch (worker.state) {
        case 'idle':
          counts.idle += 1
          break
        case 'in-flight':
          counts.inFlight += 1
          break
        case 'backoff':
          counts.backoff += 1
          break
      }
    }

    return {
      active,
      draining,
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
        worker.terminalError = false
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
    worker.terminalError = true
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
    const refill = this.config.requestedTps * FIXED_STEP_MS / 1_000
    const headTransactions = this.readerChannel.peek()?.transactions ??
      this.config.readBatchSize
    const tokenCapacity = Math.max(headTransactions, this.config.readBatchSize) +
      refill
    this.throttlerTokens = Math.min(
      tokenCapacity,
      this.throttlerTokens + refill,
    )
  }

  private drainSenderChannel(activity: StepActivity): void {
    while (this.senderChannel.peek() !== null) {
      const worker = this.nextIdleWorker()
      if (worker === null) return
      const batch = this.senderChannel.receive(activity.senderChannel)
      if (batch === null) return
      worker.batch = batch
      worker.hadAmbiguousOutcome = false
      this.startAttempt(worker, 1, activity)
    }
  }

  private flushThrottledBatch(activity: StepActivity): boolean {
    const batch = this.pendingThrottledBatch
    if (batch === null) return false

    if (this.senderChannel.capacity.applied === 0) {
      const worker = this.nextIdleWorker()
      if (worker === null) return true
      this.senderChannel.handoff(batch, activity.senderChannel)
      worker.batch = batch
      worker.hadAmbiguousOutcome = false
      this.startAttempt(worker, 1, activity)
    } else {
      if (!this.senderChannel.send(batch, activity.senderChannel)) return true
      this.drainSenderChannel(activity)
    }
    this.pendingThrottledBatch = null
    return false
  }

  private createPipelineBatch(transactions: number): SimulationBatch {
    const sequence = this.nextBatchSequence
    this.nextBatchSequence += 1
    return {
      sequence,
      identity: 'pipeline-batch-' + sequence,
      transactions,
    }
  }

  private receiveReaderChannel(activity: StepActivity): AdmissionResult {
    const availableBatches = this.readerChannel.depthBatches
    const installed = this.config.throttlerInstallationMode === 'installed'
    for (let batchIndex = 0; batchIndex < availableBatches; batchIndex += 1) {
      if (this.pendingThrottledBatch !== null) {
        const senderChannelBlocked = this.flushThrottledBatch(activity)
        if (senderChannelBlocked) return { senderChannelBlocked: true, tokenLimited: false }
      }

      const batch = this.readerChannel.peek()
      if (batch === null) break
      if (installed && this.throttlerTokens < batch.transactions) {
        return { senderChannelBlocked: false, tokenLimited: true }
      }

      const received = this.readerChannel.receive(activity.readerChannel)
      if (received === null) break
      if (installed) this.throttlerTokens -= received.transactions
      this.pendingThrottledBatch = received
      const senderChannelBlocked = this.flushThrottledBatch(activity)
      if (senderChannelBlocked) return { senderChannelBlocked: true, tokenLimited: false }
    }
    return { senderChannelBlocked: false, tokenLimited: false }
  }

  private produceReaderBatches(activity: StepActivity): ReaderProductionResult {
    const transactions = this.config.readBatchSize
    this.readerTransactionCredit = Math.min(
      this.config.readerWorkers * transactions,
      this.readerTransactionCredit +
        this.readerCapacityTps * FIXED_STEP_MS / 1_000,
    )
    if (this.readerTransactionCredit < transactions) {
      return {
        readerChannelBlocked: false,
        senderChannelBlocked: false,
        tokenLimited: false,
      }
    }

    while (this.readerTransactionCredit >= transactions) {
      if (this.readerChannel.capacity.applied === 0) {
        if (this.pendingThrottledBatch !== null) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: true,
            tokenLimited: false,
          }
        }
        if (
          this.config.throttlerInstallationMode === 'installed' &&
          this.throttlerTokens < transactions
        ) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: false,
            tokenLimited: true,
          }
        }
        const batch = this.createPipelineBatch(transactions)
        this.readerChannel.handoff(batch, activity.readerChannel)
        if (this.config.throttlerInstallationMode === 'installed') {
          this.throttlerTokens -= transactions
        }
        this.readerTransactionCredit -= transactions
        this.pendingThrottledBatch = batch
        const senderChannelBlocked = this.flushThrottledBatch(activity)
        if (senderChannelBlocked) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: true,
            tokenLimited: false,
          }
        }
      } else {
        if (
          this.readerChannel.depthBatches >=
          this.readerChannel.capacity.applied
        ) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: false,
            tokenLimited: false,
          }
        }
        const batch = this.createPipelineBatch(transactions)
        if (!this.readerChannel.send(batch, activity.readerChannel)) {
          throw new Error('reader channel capacity changed during send')
        }
        this.readerTransactionCredit -= transactions
      }
    }

    return {
      readerChannelBlocked: false,
      senderChannelBlocked: false,
      tokenLimited: false,
    }
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
      aggregate.readerChannel.inputBatches += activity.readerChannel.inputBatches
      aggregate.readerChannel.inputTransactions += activity.readerChannel.inputTransactions
      aggregate.readerChannel.outputBatches += activity.readerChannel.outputBatches
      aggregate.readerChannel.outputTransactions += activity.readerChannel.outputTransactions
      aggregate.readerChannel.handoffBatches += activity.readerChannel.handoffBatches
      aggregate.senderChannel.inputBatches += activity.senderChannel.inputBatches
      aggregate.senderChannel.inputTransactions += activity.senderChannel.inputTransactions
      aggregate.senderChannel.outputBatches += activity.senderChannel.outputBatches
      aggregate.senderChannel.outputTransactions += activity.senderChannel.outputTransactions
      aggregate.senderChannel.handoffBatches += activity.senderChannel.handoffBatches
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
