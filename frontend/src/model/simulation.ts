import type { SenderWorkerState as SenderWorkerSnapshot } from './loadgen'

export const FIXED_STEP_MS = 10
export const SNAPSHOT_INTERVAL_MS = 100
export const RETRY_MAX_ATTEMPTS = 3
export const RETRY_BACKOFF_BASE_MS = 250
export const RETRY_BACKOFF_MULTIPLIER = 2
export const RETRYABLE_STATUS_CODES = Object.freeze([408, 425, 429, 500, 502, 503, 504])

const RATE_WINDOW_STEPS = 100
const READER_TRANSACTIONS_PER_WORKER_SECOND = 50_000
const RETRY_JITTER_FACTORS = [0.8, 0.9, 1, 1.1, 1.2] as const

export interface SimulationConfig {
  readerWorkers: number
  senderWorkers: number
  requestedTps: number
  throttlerInstalled: boolean
  readBatchSize: number
  httpTimeoutMs: number
  targetDelayMs: number
  targetErrorRatePercent: number
}

export interface ChannelTelemetry {
  readonly capacity: number
  readonly depthBatches: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly inputTransactionsPerSecond: number
  readonly outputTransactionsPerSecond: number
}

export interface SenderTelemetry {
  readonly workerStates: readonly SenderWorkerSnapshot[]
}

export interface SimulationTelemetry {
  readonly elapsedMs: number
  readonly totalTransactions: number
  readonly rowsRead: number
  readonly readerTransactionsPerSecond: number
  readonly admittedTransactionsPerSecond: number
  readonly readerChannel: ChannelTelemetry
  readonly senderChannel: ChannelTelemetry
  readonly sender: SenderTelemetry
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
  inputTransactions: number
  outputTransactions: number
}

interface StepActivity {
  readerChannel: ChannelActivity
  senderChannel: ChannelActivity
}

interface AdmissionResult {
  senderChannelBlocked: boolean
  tokenLimited: boolean
}

interface ReaderProductionResult extends AdmissionResult {
  readerChannelBlocked: boolean
}

type SenderWorkerState = 'idle' | 'in-flight' | 'backoff'

interface SenderWorker {
  readonly workerId: number
  retiring: boolean
  state: SenderWorkerState
  batch: SimulationBatch | null
  attempt: number
  completeAtMs: number
  retryAtMs: number
  outcome: SimulationAttemptOutcome | null
}

function createChannelActivity(): ChannelActivity {
  return {
    inputTransactions: 0,
    outputTransactions: 0,
  }
}

function createStepActivity(): StepActivity {
  return {
    readerChannel: createChannelActivity(),
    senderChannel: createChannelActivity(),
  }
}

function createWorker(workerId: number): SenderWorker {
  return {
    workerId,
    retiring: false,
    state: 'idle',
    batch: null,
    attempt: 0,
    completeAtMs: 0,
    retryAtMs: 0,
    outcome: null,
  }
}

function roundedStepDuration(durationMs: number): number {
  const finiteDuration = Number.isFinite(durationMs) ? durationMs : FIXED_STEP_MS
  return Math.ceil(Math.max(0, finiteDuration) / FIXED_STEP_MS) * FIXED_STEP_MS
}

export function deterministicRetryDelayMs(batchSequence: number, retryNumber: 1 | 2): number {
  const nominal = RETRY_BACKOFF_BASE_MS * RETRY_BACKOFF_MULTIPLIER ** (retryNumber - 1)
  const factorIndex =
    (((batchSequence + retryNumber) % RETRY_JITTER_FACTORS.length) + RETRY_JITTER_FACTORS.length) %
    RETRY_JITTER_FACTORS.length
  return roundedStepDuration(nominal * RETRY_JITTER_FACTORS[factorIndex])
}

class StatefulChannel<TItem> {
  private readonly items: TItem[] = []
  private readonly transactionsOf: (item: TItem) => number
  readonly capacity: number
  private blockedForMs = 0
  sentTransactionsTotal = 0
  receivedTransactionsTotal = 0
  blockedSenders = 0

  constructor(capacity: number, transactionsOf: (item: TItem) => number) {
    this.capacity = capacity
    this.transactionsOf = transactionsOf
  }

  get depthBatches(): number {
    return this.items.length
  }

  get oldestBlockedSenderMs(): number {
    return this.blockedForMs
  }

  peek(): TItem | null {
    return this.items[0] ?? null
  }

  send(item: TItem, activity: ChannelActivity): boolean {
    if (this.capacity === 0 || this.depthBatches >= this.capacity) {
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
    return item
  }

  handoff(item: TItem, activity: ChannelActivity): void {
    this.recordInput(item, activity)
    this.recordOutput(item, activity)
  }

  observeBlocked(blocked: boolean, senderProgressed: boolean): void {
    this.blockedSenders = blocked ? 1 : 0
    if (blocked) {
      this.blockedForMs = senderProgressed ? FIXED_STEP_MS : this.blockedForMs + FIXED_STEP_MS
    } else {
      this.blockedForMs = 0
    }
  }

  telemetry(activity: ChannelActivity, rateSeconds: number): ChannelTelemetry {
    const inputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.inputTransactions / rateSeconds
    const outputTransactionsPerSecond =
      rateSeconds === 0 ? 0 : activity.outputTransactions / rateSeconds
    return {
      capacity: this.capacity,
      depthBatches: this.depthBatches,
      blockedSenders: this.blockedSenders,
      oldestBlockedSenderMs: this.oldestBlockedSenderMs,
      inputTransactionsPerSecond: inputTransactionsPerSecond,
      outputTransactionsPerSecond: outputTransactionsPerSecond,
    }
  }

  private recordInput(item: TItem, activity: ChannelActivity): void {
    const transactions = this.transactionsOf(item)
    this.sentTransactionsTotal += transactions
    activity.inputTransactions += transactions
  }

  private recordOutput(item: TItem, activity: ChannelActivity): void {
    const transactions = this.transactionsOf(item)
    this.receivedTransactionsTotal += transactions
    activity.outputTransactions += transactions
  }
}

export class FixedStepSimulation {
  readonly config: SimulationConfig

  private readonly readerChannel: StatefulChannel<SimulationBatch>
  private readonly senderChannel: StatefulChannel<SimulationBatch>
  private readonly activities: StepActivity[] = []
  private readonly attemptOutcomeSource: SimulationAttemptOutcomeSource | undefined
  private workers: SenderWorker[]
  private nextWorkerIndex = 0
  private readerTransactionCredit = 0
  private throttlerTokens = 0
  private pendingThrottledBatch: {
    batch: SimulationBatch
    needsTokens: boolean
  } | null = null
  private nextBatchSequence = 0
  private failureCredit = 0
  private elapsedMs = 0

  constructor(
    config: Omit<SimulationConfig, 'throttlerInstalled'> & { throttlerInstalled?: boolean },
    readerChannelCapacity: number,
    senderChannelCapacity: number,
    attemptOutcomeSource?: SimulationAttemptOutcomeSource,
  ) {
    this.config = { ...config, throttlerInstalled: config.throttlerInstalled ?? true }
    this.readerChannel = new StatefulChannel(readerChannelCapacity, (batch) => batch.transactions)
    this.senderChannel = new StatefulChannel(senderChannelCapacity, (batch) => batch.transactions)
    this.attemptOutcomeSource = attemptOutcomeSource
    this.workers = Array.from({ length: config.senderWorkers }, (_, ordinal) =>
      createWorker(ordinal),
    )
    this.nextWorkerIndex = 0
  }

  advanceStep(): void {
    this.elapsedMs += FIXED_STEP_MS
    const activity = createStepActivity()

    this.completeDueAttempts()
    this.startDueRetries()
    this.finalizeSenderScaleDown()
    this.refillThrottlerTokens()
    this.drainSenderChannel(activity)
    let admission = this.flushThrottledBatch(activity)
    if (!admission.senderChannelBlocked && !admission.tokenLimited) {
      admission = this.receiveReaderChannel(activity)
    }
    const production = this.produceReaderBatches(activity)

    this.readerChannel.observeBlocked(
      production.readerChannelBlocked,
      activity.readerChannel.inputTransactions > 0,
    )
    this.senderChannel.observeBlocked(
      admission.senderChannelBlocked || production.senderChannelBlocked,
      activity.senderChannel.inputTransactions > 0,
    )

    this.activities.push(activity)
    if (this.activities.length > RATE_WINDOW_STEPS) this.activities.shift()
  }

  updateConfig(
    values: Partial<
      Pick<
        SimulationConfig,
        'readerWorkers' | 'senderWorkers' | 'requestedTps' | 'throttlerInstalled'
      >
    >,
  ): void {
    const senderWorkers = values.senderWorkers
    Object.assign(this.config, values)
    if (senderWorkers !== undefined) {
      this.requestSenderWorkerCount(senderWorkers)
    }
    if (values.requestedTps === 0 || values.throttlerInstalled === false) {
      this.throttlerTokens = 0
    }
    if (values.throttlerInstalled === false && this.pendingThrottledBatch !== null) {
      this.pendingThrottledBatch.needsTokens = true
    }
    this.readerTransactionCredit = Math.min(
      this.readerTransactionCredit,
      this.config.readerWorkers * this.config.readBatchSize,
    )
  }

  telemetry(): SimulationTelemetry {
    const aggregate = this.aggregateActivity()
    const rateSeconds = (RATE_WINDOW_STEPS * FIXED_STEP_MS) / 1_000
    const readerChannel = this.readerChannel.telemetry(aggregate.readerChannel, rateSeconds)
    const senderChannel = this.senderChannel.telemetry(aggregate.senderChannel, rateSeconds)

    return {
      elapsedMs: this.elapsedMs,
      totalTransactions: this.readerChannel.receivedTransactionsTotal,
      rowsRead: this.readerChannel.sentTransactionsTotal,
      readerTransactionsPerSecond: readerChannel.inputTransactionsPerSecond,
      admittedTransactionsPerSecond: readerChannel.outputTransactionsPerSecond,
      readerChannel,
      senderChannel,
      sender: {
        workerStates: Object.freeze(
          this.workers.map((worker): SenderWorkerSnapshot =>
            Object.freeze({
              activity: worker.state === 'in-flight' ? 'inFlight' : worker.state,
              draining: worker.retiring,
            }),
          ),
        ),
      },
    }
  }

  private get readerCapacityTps(): number {
    return this.config.readerWorkers * READER_TRANSACTIONS_PER_WORKER_SECOND
  }

  private completeDueAttempts(): void {
    for (const worker of this.workers) {
      if (worker.state !== 'in-flight' || worker.completeAtMs > this.elapsedMs) {
        continue
      }
      this.completeAttempt(worker)
    }
  }

  private completeAttempt(worker: SenderWorker): void {
    const batch = worker.batch
    const outcome = worker.outcome
    if (batch === null || outcome === null) {
      throw new Error('in-flight worker must own a batch and outcome')
    }

    if (outcome.kind === 'http-response') {
      if (outcome.statusCode >= 200 && outcome.statusCode < 300) {
        this.releaseWorker(worker)
        return
      }
      if (
        RETRYABLE_STATUS_CODES.includes(outcome.statusCode) &&
        worker.attempt < RETRY_MAX_ATTEMPTS
      ) {
        this.scheduleRetry(worker)
        return
      }
      this.releaseWorker(worker)
      return
    }

    if (worker.attempt < RETRY_MAX_ATTEMPTS) {
      this.scheduleRetry(worker)
      return
    }
    this.releaseWorker(worker)
  }

  private scheduleRetry(worker: SenderWorker): void {
    const batch = worker.batch
    if (batch === null || worker.attempt >= RETRY_MAX_ATTEMPTS) {
      throw new Error('retry requires an owned batch and remaining attempt')
    }
    const retryNumber = worker.attempt as 1 | 2
    worker.state = 'backoff'
    worker.retryAtMs = this.elapsedMs + deterministicRetryDelayMs(batch.sequence, retryNumber)
    worker.completeAtMs = 0
    worker.outcome = null
  }

  private releaseWorker(worker: SenderWorker): void {
    worker.state = 'idle'
    worker.batch = null
    worker.attempt = 0
    worker.completeAtMs = 0
    worker.retryAtMs = 0
    worker.outcome = null
  }

  private startDueRetries(): void {
    for (const worker of this.workers) {
      if (worker.state !== 'backoff' || worker.retryAtMs > this.elapsedMs) {
        continue
      }
      this.startAttempt(worker, worker.attempt + 1)
    }
  }

  private refillThrottlerTokens(): void {
    if (!this.config.throttlerInstalled) return

    const refill = (this.config.requestedTps * FIXED_STEP_MS) / 1_000
    const headTransactions = this.readerChannel.peek()?.transactions ?? this.config.readBatchSize
    const tokenCapacity = Math.max(headTransactions, this.config.readBatchSize) + refill
    this.throttlerTokens = Math.min(tokenCapacity, this.throttlerTokens + refill)
  }

  private tryTakeThrottlerTokens(transactions: number): boolean {
    if (!this.config.throttlerInstalled) return true
    if (this.throttlerTokens < transactions) return false
    this.throttlerTokens -= transactions
    return true
  }

  private drainSenderChannel(activity: StepActivity): void {
    while (this.senderChannel.peek() !== null) {
      const worker = this.nextIdleWorker()
      if (worker === null) return
      const batch = this.senderChannel.receive(activity.senderChannel)
      if (batch === null) return
      worker.batch = batch
      this.startAttempt(worker, 1)
    }
  }

  private forwardThrottledBatch(batch: SimulationBatch, activity: StepActivity): AdmissionResult {
    this.pendingThrottledBatch = { batch, needsTokens: !this.config.throttlerInstalled }
    return this.flushThrottledBatch(activity)
  }

  private flushThrottledBatch(activity: StepActivity): AdmissionResult {
    const pending = this.pendingThrottledBatch
    if (pending === null) return { senderChannelBlocked: false, tokenLimited: false }

    const { batch } = pending
    if (pending.needsTokens && this.config.throttlerInstalled) {
      if (!this.tryTakeThrottlerTokens(batch.transactions)) {
        return { senderChannelBlocked: false, tokenLimited: true }
      }
      pending.needsTokens = false
    }
    if (this.senderChannel.capacity === 0) {
      const worker = this.nextIdleWorker()
      if (worker === null) return { senderChannelBlocked: true, tokenLimited: false }
      this.senderChannel.handoff(batch, activity.senderChannel)
      worker.batch = batch
      this.startAttempt(worker, 1)
    } else {
      if (!this.senderChannel.send(batch, activity.senderChannel)) {
        return { senderChannelBlocked: true, tokenLimited: false }
      }
      this.drainSenderChannel(activity)
    }
    this.pendingThrottledBatch = null
    return { senderChannelBlocked: false, tokenLimited: false }
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
    for (let batchIndex = 0; batchIndex < availableBatches; batchIndex += 1) {
      const batch = this.readerChannel.peek()
      if (batch === null) break
      if (!this.tryTakeThrottlerTokens(batch.transactions)) {
        return { senderChannelBlocked: false, tokenLimited: true }
      }

      const received = this.readerChannel.receive(activity.readerChannel)
      if (received === null) break
      const result = this.forwardThrottledBatch(received, activity)
      if (result.senderChannelBlocked || result.tokenLimited) return result
    }
    return { senderChannelBlocked: false, tokenLimited: false }
  }

  private produceReaderBatches(activity: StepActivity): ReaderProductionResult {
    const transactions = this.config.readBatchSize
    this.readerTransactionCredit = Math.min(
      this.config.readerWorkers * transactions,
      this.readerTransactionCredit + (this.readerCapacityTps * FIXED_STEP_MS) / 1_000,
    )
    if (this.readerTransactionCredit < transactions) {
      return {
        readerChannelBlocked: false,
        senderChannelBlocked: false,
        tokenLimited: false,
      }
    }

    while (this.readerTransactionCredit >= transactions) {
      if (this.readerChannel.capacity === 0) {
        if (this.pendingThrottledBatch !== null) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: false,
            tokenLimited: false,
          }
        }
        if (!this.tryTakeThrottlerTokens(transactions)) {
          return {
            readerChannelBlocked: true,
            senderChannelBlocked: false,
            tokenLimited: true,
          }
        }
        const batch = this.createPipelineBatch(transactions)
        this.readerChannel.handoff(batch, activity.readerChannel)
        this.readerTransactionCredit -= transactions
        const result = this.forwardThrottledBatch(batch, activity)
        if (result.senderChannelBlocked || result.tokenLimited) {
          return {
            readerChannelBlocked: true,
            ...result,
          }
        }
      } else {
        if (this.readerChannel.depthBatches >= this.readerChannel.capacity) {
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

  private startAttempt(worker: SenderWorker, attempt: number): void {
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
    const selected = this.attemptOutcomeSource?.(context) ?? this.defaultAttemptOutcome(context)
    const latencyMs = roundedStepDuration(selected.latencyMs)
    const outcome =
      selected.kind === 'http-response' ? { ...selected, latencyMs } : { ...selected, latencyMs }

    worker.state = 'in-flight'
    worker.attempt = attempt
    worker.completeAtMs = this.elapsedMs + latencyMs
    worker.retryAtMs = 0
    worker.outcome = outcome
  }

  private defaultAttemptOutcome(context: SimulationAttemptContext): SimulationAttemptOutcome {
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
    for (let index = this.workers.length - retireCount; index < this.workers.length; index += 1) {
      this.workers[index].retiring = true
    }
    this.finalizeSenderScaleDown()
  }

  private finalizeSenderScaleDown(): void {
    const retiring = this.workers.filter((worker) => worker.retiring)
    if (retiring.length === 0 || retiring.some((worker) => worker.state !== 'idle')) {
      return
    }
    this.workers = this.workers.filter((worker) => !worker.retiring)
    this.nextWorkerIndex %= this.workers.length
  }

  private aggregateActivity(): StepActivity {
    const aggregate = createStepActivity()
    for (const activity of this.activities) {
      aggregate.readerChannel.inputTransactions += activity.readerChannel.inputTransactions
      aggregate.readerChannel.outputTransactions += activity.readerChannel.outputTransactions
      aggregate.senderChannel.inputTransactions += activity.senderChannel.inputTransactions
      aggregate.senderChannel.outputTransactions += activity.senderChannel.outputTransactions
    }
    return aggregate
  }
}
