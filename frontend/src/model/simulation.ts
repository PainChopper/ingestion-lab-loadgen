import type { QueueFlowState, QueueTrend } from './loadgen'

export const FIXED_STEP_MS = 10
export const SNAPSHOT_INTERVAL_MS = 100

const RATE_WINDOW_STEPS = 100
const BACKPRESSURE_ONSET_MS = 300
const BACKPRESSURE_RELEASE_MS = 200
const READER_TRANSACTIONS_PER_WORKER_SECOND = 50_000
const REQUEST_SLOTS_PER_SENDER_WORKER = 2

export interface SimulationConfig {
  readerWorkers: number
  senderWorkers: number
  requestedTps: number
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
  readonly flowState: QueueFlowState
}

export interface HttpTelemetry {
  readonly requestsStartedTotal: number
  readonly requestsCompletedTotal: number
  readonly requestsSucceededTotal: number
  readonly requestsFailedTotal: number
  readonly requestsTimedOutTotal: number
  readonly successfulTransactionsTotal: number
  readonly failedTransactionsTotal: number
  readonly inFlightRequests: number
  readonly startedTransactionsPerSecond: number
  readonly completedTransactionsPerSecond: number
  readonly succeededTransactionsPerSecond: number
  readonly latestStatusCode: number | null
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
  readonly queue1: QueueTelemetry
  readonly queue2: QueueTelemetry
  readonly http: HttpTelemetry
}

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
  httpCompletedTransactions: number
  httpSucceededTransactions: number
}

interface InFlightRequest {
  readonly completeAtMs: number
  readonly transactions: number
  readonly outcome: 'success' | 'failure' | 'timeout'
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
    httpCompletedTransactions: 0,
    httpSucceededTransactions: 0,
  }
}

class StatefulQueue {
  private readonly items: number[] = []
  private appliedCapacity: number
  private previewCapacity: number | null = null
  private pendingCapacity: number | null = null
  private blockedForMs = 0
  private clearForMs = 0
  private backpressure = false

  enqueuedBatchesTotal = 0
  enqueuedTransactionsTotal = 0
  dequeuedBatchesTotal = 0
  dequeuedTransactionsTotal = 0
  handoffBatchesTotal = 0
  blockedMsTotal = 0
  blockedSenders = 0

  constructor(capacity: number) {
    this.appliedCapacity = capacity
  }

  get depthBatches(): number {
    return this.items.length
  }

  get queuedTransactions(): number {
    return this.items.reduce((total, transactions) => total + transactions, 0)
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

  peek(): number | null {
    return this.items[0] ?? null
  }

  enqueue(transactions: number, activity: QueueActivity): boolean {
    if (this.appliedCapacity === 0 || this.depthBatches >= this.appliedCapacity) {
      return false
    }
    this.items.push(transactions)
    this.recordInput(transactions, activity)
    return true
  }

  dequeue(activity: QueueActivity): number | null {
    const transactions = this.items.shift()
    if (transactions === undefined) return null
    this.recordOutput(transactions, activity)
    this.applyPendingCapacity()
    return transactions
  }

  handoff(transactions: number, activity: QueueActivity): void {
    this.recordInput(transactions, activity)
    this.recordOutput(transactions, activity)
    this.handoffBatchesTotal += 1
    activity.handoffBatches += 1
  }

  observeBlocked(blocked: boolean): void {
    this.blockedSenders = blocked ? 1 : 0
    if (blocked) {
      this.blockedForMs += FIXED_STEP_MS
      this.blockedMsTotal += FIXED_STEP_MS
      this.clearForMs = 0
      if (this.blockedForMs >= BACKPRESSURE_ONSET_MS) {
        this.backpressure = true
      }
      return
    }

    this.blockedForMs = 0
    if (!this.backpressure) {
      this.clearForMs = 0
      return
    }
    this.clearForMs += FIXED_STEP_MS
    if (this.clearForMs >= BACKPRESSURE_RELEASE_MS) {
      this.backpressure = false
      this.clearForMs = 0
    }
  }

  telemetry(activity: QueueActivity, rateSeconds: number, running: boolean): QueueTelemetry {
    const inputBatchesPerSecond = rateSeconds === 0 ? 0 : activity.inputBatches / rateSeconds
    const outputBatchesPerSecond = rateSeconds === 0 ? 0 : activity.outputBatches / rateSeconds
    const inputTransactionsPerSecond = rateSeconds === 0 ? 0 : activity.inputTransactions / rateSeconds
    const outputTransactionsPerSecond = rateSeconds === 0 ? 0 : activity.outputTransactions / rateSeconds
    const trend: QueueTrend = !running
      ? 'steady'
      : inputTransactionsPerSecond > outputTransactionsPerSecond
        ? 'rising'
        : inputTransactionsPerSecond < outputTransactionsPerSecond
          ? 'falling'
          : 'steady'
    const flowState: QueueFlowState = !running
      ? 'stopped'
      : this.backpressure
        ? 'backpressure'
        : this.appliedCapacity > 0 && this.depthBatches >= this.appliedCapacity
          ? 'near-limit'
          : 'normal'

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
      flowState,
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
    this.clearForMs = 0
    this.backpressure = false
    this.enqueuedBatchesTotal = 0
    this.enqueuedTransactionsTotal = 0
    this.dequeuedBatchesTotal = 0
    this.dequeuedTransactionsTotal = 0
    this.handoffBatchesTotal = 0
    this.blockedMsTotal = 0
    this.blockedSenders = 0
  }

  private recordInput(transactions: number, activity: QueueActivity): void {
    this.enqueuedBatchesTotal += 1
    this.enqueuedTransactionsTotal += transactions
    activity.inputBatches += 1
    activity.inputTransactions += transactions
  }

  private recordOutput(transactions: number, activity: QueueActivity): void {
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

  private readonly queue1: StatefulQueue
  private readonly queue2: StatefulQueue
  private readonly activities: StepActivity[] = []
  private readonly inFlight: InFlightRequest[] = []
  private readerTransactionCredit = 0
  private throttlerTokens = 0
  private throttlerBufferedTransactions = 0
  private failureCredit = 0
  private latestStatusCode: number | null = null
  private requestStartedTotal = 0
  private requestCompletedTotal = 0
  private requestSucceededTotal = 0
  private requestFailedTotal = 0
  private requestTimedOutTotal = 0
  private successfulTransactionsTotal = 0
  private failedTransactionsTotal = 0
  private elapsedMs = 0
  private limitedMs = 0

  constructor(
    config: SimulationConfig,
    queue1Capacity: number,
    queue2Capacity: number,
  ) {
    this.config = { ...config }
    this.queue1 = new StatefulQueue(queue1Capacity)
    this.queue2 = new StatefulQueue(queue2Capacity)
  }

  advanceStep(): void {
    this.elapsedMs += FIXED_STEP_MS
    const activity = createStepActivity()

    this.completeRequests(activity)
    this.refillThrottlerTokens()
    this.drainQueue2(activity)
    const queue2Blocked = this.flushThrottlerBuffer(activity)
    this.receiveQueue1(activity)
    const queue1Blocked = this.produceReaderBatches(activity)

    this.queue1.observeBlocked(queue1Blocked)
    this.queue2.observeBlocked(queue2Blocked)
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
    Object.assign(this.config, values)
    if (values.requestedTps === 0) this.throttlerTokens = 0
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
    this.inFlight.length = 0
    this.readerTransactionCredit = 0
    this.throttlerTokens = 0
    this.throttlerBufferedTransactions = 0
    this.failureCredit = 0
    this.latestStatusCode = null
    this.requestStartedTotal = 0
    this.requestCompletedTotal = 0
    this.requestSucceededTotal = 0
    this.requestFailedTotal = 0
    this.requestTimedOutTotal = 0
    this.successfulTransactionsTotal = 0
    this.failedTransactionsTotal = 0
    this.elapsedMs = 0
    this.limitedMs = 0
  }

  telemetry(running: boolean): SimulationTelemetry {
    const aggregate = this.aggregateActivity()
    const rateSeconds = RATE_WINDOW_STEPS * FIXED_STEP_MS / 1_000
    const queue1 = this.queue1.telemetry(aggregate.queue1, rateSeconds, running)
    const queue2 = this.queue2.telemetry(aggregate.queue2, rateSeconds, running)
    const divisor = rateSeconds === 0 ? 1 : rateSeconds

    return {
      elapsedMs: this.elapsedMs,
      totalTransactions: this.queue1.dequeuedTransactionsTotal,
      limitedMs: this.limitedMs,
      readerCapacityTps: this.readerCapacityTps,
      readerTransactionsPerSecond: queue1.inputTransactionsPerSecond,
      admittedTransactionsPerSecond: queue1.outputTransactionsPerSecond,
      attemptedTransactionsPerSecond: queue2.outputTransactionsPerSecond,
      acceptedTransactionsPerSecond: running
        ? aggregate.httpSucceededTransactions / divisor
        : 0,
      queue1,
      queue2,
      http: {
        requestsStartedTotal: this.requestStartedTotal,
        requestsCompletedTotal: this.requestCompletedTotal,
        requestsSucceededTotal: this.requestSucceededTotal,
        requestsFailedTotal: this.requestFailedTotal,
        requestsTimedOutTotal: this.requestTimedOutTotal,
        successfulTransactionsTotal: this.successfulTransactionsTotal,
        failedTransactionsTotal: this.failedTransactionsTotal,
        inFlightRequests: this.inFlight.length,
        startedTransactionsPerSecond: running
          ? aggregate.httpStartedTransactions / divisor
          : 0,
        completedTransactionsPerSecond: running
          ? aggregate.httpCompletedTransactions / divisor
          : 0,
        succeededTransactionsPerSecond: running
          ? aggregate.httpSucceededTransactions / divisor
          : 0,
        latestStatusCode: this.latestStatusCode,
      },
    }
  }

  private get readerCapacityTps(): number {
    return this.config.readerWorkers * READER_TRANSACTIONS_PER_WORKER_SECOND
  }

  private completeRequests(activity: StepActivity): void {
    let writeIndex = 0
    for (const request of this.inFlight) {
      if (request.completeAtMs > this.elapsedMs) {
        this.inFlight[writeIndex] = request
        writeIndex += 1
        continue
      }

      this.requestCompletedTotal += 1
      activity.httpCompletedTransactions += request.transactions
      if (request.outcome === 'success') {
        this.requestSucceededTotal += 1
        this.successfulTransactionsTotal += request.transactions
        activity.httpSucceededTransactions += request.transactions
        this.latestStatusCode = 200
      } else {
        this.requestFailedTotal += 1
        this.failedTransactionsTotal += request.transactions
        if (request.outcome === 'timeout') this.requestTimedOutTotal += 1
        this.latestStatusCode = request.outcome === 'timeout' ? 504 : 503
      }
    }
    this.inFlight.length = writeIndex
  }

  private refillThrottlerTokens(): void {
    const headTransactions = this.queue1.peek() ?? this.config.readBatchSize
    const tokenCapacity = Math.max(headTransactions, this.config.readBatchSize)
    this.throttlerTokens = Math.min(
      tokenCapacity,
      this.throttlerTokens + this.config.requestedTps * FIXED_STEP_MS / 1_000,
    )
  }

  private drainQueue2(activity: StepActivity): void {
    while (this.availableRequestSlots > 0) {
      const transactions = this.queue2.dequeue(activity.queue2)
      if (transactions === null) return
      this.startRequest(transactions, activity)
    }
  }

  private flushThrottlerBuffer(activity: StepActivity): boolean {
    let blocked = false
    while (this.throttlerBufferedTransactions >= this.config.httpBatchSize) {
      const transactions = this.config.httpBatchSize
      if (this.queue2.capacity.applied === 0) {
        if (this.availableRequestSlots === 0) {
          blocked = true
          break
        }
        this.queue2.handoff(transactions, activity.queue2)
        this.startRequest(transactions, activity)
      } else if (!this.queue2.enqueue(transactions, activity.queue2)) {
        blocked = true
        break
      }
      this.throttlerBufferedTransactions -= transactions
    }
    return blocked
  }

  private receiveQueue1(activity: StepActivity): void {
    if (this.throttlerBufferedTransactions !== 0) return
    const transactions = this.queue1.peek()
    if (transactions === null || this.throttlerTokens < transactions) return
    const received = this.queue1.dequeue(activity.queue1)
    if (received === null) return
    this.throttlerTokens -= received
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
          this.throttlerTokens < transactions
        ) {
          return true
        }
        this.queue1.handoff(transactions, activity.queue1)
        this.throttlerTokens -= transactions
        this.throttlerBufferedTransactions += transactions
      } else if (!this.queue1.enqueue(transactions, activity.queue1)) {
        return true
      }

      this.readerTransactionCredit -= transactions
    }

    return false
  }

  private startRequest(transactions: number, activity: StepActivity): void {
    const targetLatencyMs = this.config.targetDelayMs + 5
    const timedOut = targetLatencyMs > this.config.httpTimeoutMs
    const latencyMs = timedOut ? this.config.httpTimeoutMs : targetLatencyMs
    const outcome = timedOut
      ? 'timeout'
      : this.nextRequestFails()
        ? 'failure'
        : 'success'
    const completeAtMs =
      this.elapsedMs + Math.ceil(latencyMs / FIXED_STEP_MS) * FIXED_STEP_MS

    this.inFlight.push({ completeAtMs, transactions, outcome })
    this.requestStartedTotal += 1
    activity.httpStartedTransactions += transactions
  }

  private nextRequestFails(): boolean {
    this.failureCredit += this.config.targetErrorRatePercent
    if (this.failureCredit < 100) return false
    this.failureCredit -= 100
    return true
  }

  private get availableRequestSlots(): number {
    return Math.max(
      0,
      this.config.senderWorkers * REQUEST_SLOTS_PER_SENDER_WORKER -
        this.inFlight.length,
    )
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
      aggregate.httpCompletedTransactions += activity.httpCompletedTransactions
      aggregate.httpSucceededTransactions += activity.httpSucceededTransactions
    }
    return aggregate
  }
}
