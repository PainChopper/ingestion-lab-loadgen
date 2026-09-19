import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type {
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  LoadgenPolicySnapshot,
  QueueTelemetrySnapshot,
  RunState,
} from '../model/loadgen'
import { HttpAdapter } from './HttpAdapter'

interface TestWireSnapshot {
  readonly runState: RunState
  readonly elapsedMs: number
  readonly startError: string | null
  readonly totalTransactions: number
  readonly policy: LoadgenPolicySnapshot
  readonly readerWorkers: number
  readonly senderWorkers: number
  readonly readerReadTps: number
  readonly readerReadBatchSize: number
  readonly readerRowsRead: number
  readonly readerSource: string | null
  readonly queue1Capacity: number
  readonly queue1EnqueuedBatchesTotal: number
  readonly queue1EnqueuedTransactionsTotal: number
  readonly queue1DequeuedBatchesTotal: number
  readonly queue1DequeuedTransactionsTotal: number
  readonly queue1DepthBatches: number
  readonly queue1QueuedTransactions: number
  readonly queue1BlockedSenders: number
  readonly queue1OldestBlockedSenderMs: number
  readonly queue1BlockedMs: number
  readonly queue1InputBatchesPerSecond: number
  readonly queue1InputTransactionsPerSecond: number
  readonly queue1OutputBatchesPerSecond: number
  readonly queue1OutputTransactionsPerSecond: number
}

interface MockResponseOptions {
  readonly status?: number
  readonly contentType?: string | null
  readonly jsonError?: Error
}

const VALID_WIRE: TestWireSnapshot = {
  runState: 'running',
  elapsedMs: 12_345,
  startError: null,
  totalTransactions: 42_000,
  policy: {
    readerReadBatchSize: {
      default: 50_000,
      min: 1_000,
      max: 100_000,
      step: 1_000,
      unit: 'transactions',
      mutability: 'idle-only',
    },
    queue1Capacity: {
      default: 2,
      allowed: [0, 1, 2, 8, 16, 64, 8_192],
      unit: 'batches',
      mutability: 'idle-only',
    },
  },
  readerWorkers: 1,
  senderWorkers: 0,
  readerReadTps: 3_500.5,
  readerReadBatchSize: 50_000,
  readerRowsRead: 14_000,
  readerSource: 'MBD-mini/trx/part/input.parquet',
  queue1Capacity: 8,
  queue1EnqueuedBatchesTotal: 11,
  queue1EnqueuedTransactionsTotal: 550_000,
  queue1DequeuedBatchesTotal: 5,
  queue1DequeuedTransactionsTotal: 250_000,
  queue1DepthBatches: 6,
  queue1QueuedTransactions: 300_000,
  queue1BlockedSenders: 1,
  queue1OldestBlockedSenderMs: 450,
  queue1BlockedMs: 1_600,
  queue1InputBatchesPerSecond: 2.5,
  queue1InputTransactionsPerSecond: 125_000.5,
  queue1OutputBatchesPerSecond: 1.25,
  queue1OutputTransactionsPerSecond: 62_500.25,
}

const SNAPSHOT_ENDPOINT = '/api/loadgen/snapshot'
const COMMAND_ENDPOINT = '/api/loadgen/commands'
const NOW_MS = Date.parse('2026-08-20T12:00:00Z')

let fetchMock: ReturnType<typeof vi.fn>

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, resolve, reject }
}

function pendingResponse(): Promise<Response> {
  return new Promise(() => undefined)
}

function mockResponse(
  body: unknown,
  options: MockResponseOptions = {},
): Response {
  const headers = new Headers()
  const contentType = options.contentType === undefined
    ? 'application/json'
    : options.contentType
  if (contentType !== null) headers.set('Content-Type', contentType)

  const json = options.jsonError === undefined
    ? vi.fn().mockResolvedValue(body)
    : vi.fn().mockRejectedValue(options.jsonError)

  return {
    status: options.status ?? 200,
    headers,
    json,
  } as unknown as Response
}

function mockCommandResponse(status = 204): Response {
  return { status } as Response
}

function responseWithThrowingRepresentation(status: number) {
  const headers = vi.fn(() => { throw new Error('headers must be ignored') })
  const body = vi.fn(() => { throw new Error('body must be ignored') })
  const json = vi.fn(() => { throw new Error('json must be ignored') })
  const text = vi.fn(() => { throw new Error('text must be ignored') })
  const response = { status }

  Object.defineProperties(response, {
    headers: { get: headers },
    body: { get: body },
    json: { get: json },
    text: { get: text },
  })

  return {
    response: response as Response,
    accessors: { headers, body, json, text },
  }
}

function snapshotFetchCalls() {
  return fetchMock.mock.calls.filter(([input]) => input === SNAPSHOT_ENDPOINT)
}

function commandFetchCalls() {
  return fetchMock.mock.calls.filter(([input]) => input === COMMAND_ENDPOINT)
}

async function flushPoll(): Promise<void> {
  for (let index = 0; index < 6; index += 1) {
    await Promise.resolve()
  }
}

function control(
  unit: string,
  applied: number | null = null,
): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: applied ?? 0,
    max: applied ?? 0,
    step: 1,
    unit,
    applyMode: 'unavailable',
  }
}

function readBatchSizeControl(
  value: number | null,
  policy: LoadgenPolicySnapshot['readerReadBatchSize'] | null,
  connectionState: ConnectionState,
  runState: RunState,
): NumericControlSnapshot {
  if (policy === null) return control('transactions')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: policy.min,
    max: policy.max,
    step: policy.step,
    unit: policy.unit,
    applyMode: connectionState === 'connected' && runState === 'idle'
      ? 'immediate'
      : 'unavailable',
  }
}

function neutralQueue(
  id: QueueTelemetrySnapshot['id'],
  from: QueueTelemetrySnapshot['from'],
  to: QueueTelemetrySnapshot['to'],
): QueueTelemetrySnapshot {
  return {
    id,
    from,
    to,
    capacity: control('batches'),
    enqueuedBatchesTotal: 0,
    enqueuedTransactionsTotal: 0,
    dequeuedBatchesTotal: 0,
    dequeuedTransactionsTotal: 0,
    depthBatches: null,
    queuedTransactions: null,
    handoffBatches: 0,
    handoffBatchesTotal: 0,
    blockedSenders: 0,
    oldestBlockedSenderMs: 0,
    inputBatchesPerSecond: 0,
    outputBatchesPerSecond: 0,
    inputTransactionsPerSecond: 0,
    outputTransactionsPerSecond: 0,
    inputTps: null,
    outputTps: null,
    throughputTps: null,
    blockedMs: null,
    trend: 'unknown',
  }
}

function queue1(
  wire: TestWireSnapshot | null,
  connectionState: ConnectionState,
): QueueTelemetrySnapshot {
  const queue = neutralQueue(
    'reader-to-throttler',
    'reader',
    'throttler',
  )
  if (wire === null) return queue

  return {
    ...queue,
    capacity: {
      ...control('batches', wire.queue1Capacity),
      min: wire.policy.queue1Capacity.allowed[0]!,
      max: wire.policy.queue1Capacity.allowed.at(-1)!,
      applyMode: connectionState === 'connected' && wire.runState === 'idle'
        ? 'immediate'
        : 'unavailable',
    },
    depthBatches: wire.queue1DepthBatches,
    queuedTransactions: wire.queue1QueuedTransactions,
    enqueuedBatchesTotal: wire.queue1EnqueuedBatchesTotal,
    enqueuedTransactionsTotal: wire.queue1EnqueuedTransactionsTotal,
    dequeuedBatchesTotal: wire.queue1DequeuedBatchesTotal,
    dequeuedTransactionsTotal: wire.queue1DequeuedTransactionsTotal,
    inputBatchesPerSecond: wire.queue1InputBatchesPerSecond,
    inputTransactionsPerSecond: wire.queue1InputTransactionsPerSecond,
    outputBatchesPerSecond: wire.queue1OutputBatchesPerSecond,
    outputTransactionsPerSecond: wire.queue1OutputTransactionsPerSecond,
    inputTps: wire.queue1InputTransactionsPerSecond,
    outputTps: wire.queue1OutputTransactionsPerSecond,
    throughputTps: wire.queue1OutputTransactionsPerSecond,
    blockedSenders: wire.queue1BlockedSenders,
    oldestBlockedSenderMs: wire.queue1OldestBlockedSenderMs,
    blockedMs: wire.queue1BlockedMs,
  }
}

function expectedSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: TestWireSnapshot | null = null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.runState ?? 'idle'

  return {
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.elapsedMs ?? 0,
    startError: wire?.startError ?? null,
    totalTransactions: wire?.totalTransactions ?? 0,
    policy: wire?.policy ?? null,
    reader: {
      id: 'reader',
      workers: control('workers', wire?.readerWorkers ?? null),
      readBatchSize: readBatchSizeControl(
        wire?.readerReadBatchSize ?? null,
        wire?.policy.readerReadBatchSize ?? null,
        connectionState,
        runState,
      ),
      readTps: wire?.readerReadTps ?? null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: wire?.readerRowsRead ?? null,
      source: wire?.readerSource ?? null,
      state: runState,
    },
    throttler: {
      id: 'throttler',
      requestedTps: control('tx/s'),
      installationMode: {
        applied: null,
        pending: null,
        applyMode: 'unavailable',
        writable: false,
        unavailableReason: 'Недоступно в HTTP snapshot mode',
      },
      admittedTps: null,
      limitedMs: null,
      state: runState,
    },
    queue1: queue1(wire, connectionState),
    queue2: neutralQueue(
      'throttler-to-sender',
      'throttler',
      'sender',
    ),
    sender: {
      id: 'sender',
      workers: control('workers', wire?.senderWorkers ?? null),
      httpBatchSize: control('tx'),
      timeoutMs: control('ms'),
      workerStates: { idle: 0, inFlight: 0, backoff: 0 },
      workerSlots: null,
      retryPolicy: null,
      attemptedTps: null,
      retryAttemptedTps: null,
      terminalFailedTps: null,
      inFlightRequests: null,
      attemptsStartedTotal: 0,
      retryAttemptsStartedTotal: 0,
      successfulResponses: null,
      failedResponses: null,
      retries: null,
      timeoutsTotal: 0,
      terminalFailedBatchesTotal: 0,
      terminalFailedTransactionsTotal: 0,
      ambiguousTimeoutTransactionsTotal: 0,
      duplicateRiskTransactionsTotal: 0,
      ambiguousTerminalTransactionsTotal: 0,
      state: runState,
    },
    http: {
      id: 'http',
      connectionState: 'disconnected',
      statusCode: null,
      lastOutcome: null,
      throughputTps: null,
      inFlightRequests: null,
      requestsStartedTotal: 0,
      requestsCompletedTotal: 0,
      requestsSucceededTotal: 0,
      requestsFailedTotal: 0,
      requestsTimedOutTotal: 0,
      networkErrorsTotal: 0,
      latencyP95Ms: null,
    },
    target: {
      id: 'target',
      endpoint: null,
      artificialDelayMs: control('ms'),
      errorRatePercent: control('%'),
      acceptedTps: null,
      rejectedTps: null,
      latencyP95Ms: null,
      http200Responses: null,
      http503Responses: null,
      connectionState: 'disconnected',
    },
  }
}

function expectDeepFrozen(value: unknown, seen = new Set<object>()): void {
  if (value === null || typeof value !== 'object' || seen.has(value)) return
  seen.add(value)
  expect(Object.isFrozen(value)).toBe(true)
  Object.values(value).forEach((child) => expectDeepFrozen(child, seen))
}

const malformedCases: ReadonlyArray<{
  readonly name: string
  readonly result: () => Promise<Response>
}> = [
  {
    name: 'missing key',
    result: async () => mockResponse({
      runState: 'running',
      totalTransactions: 1,
      readerWorkers: 1,
    }),
  },
  {
    name: 'extra key',
    result: async () => mockResponse({ ...VALID_WIRE, extra: true }),
  },
  {
    name: 'missing policy',
    result: async () => {
      const { policy: _policy, ...withoutPolicy } = VALID_WIRE
      return mockResponse(withoutPolicy)
    },
  },
  {
    name: 'malformed queue1 policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: {
        ...VALID_WIRE.policy,
        queue1Capacity: {
          ...VALID_WIRE.policy.queue1Capacity,
          allowed: [0, 2, 2],
        },
      },
    }),
  },
  { name: 'null body', result: async () => mockResponse(null) },
  { name: 'array body', result: async () => mockResponse([VALID_WIRE]) },
  {
    name: 'invalid runState',
    result: async () => mockResponse({ ...VALID_WIRE, runState: 'resetting' }),
  },
  {
    name: 'numeric string',
    result: async () => mockResponse({
      ...VALID_WIRE,
      totalTransactions: '42000',
    }),
  },
  {
    name: 'negative elapsed time',
    result: async () => mockResponse({ ...VALID_WIRE, elapsedMs: -1 }),
  },
  {
    name: 'fractional elapsed time',
    result: async () => mockResponse({ ...VALID_WIRE, elapsedMs: 0.5 }),
  },
  {
    name: 'unsafe elapsed time',
    result: async () => mockResponse({
      ...VALID_WIRE,
      elapsedMs: Number.MAX_SAFE_INTEGER + 1,
    }),
  },
  {
    name: 'empty start error',
    result: async () => mockResponse({ ...VALID_WIRE, startError: '' }),
  },
  {
    name: 'invalid start error',
    result: async () => mockResponse({ ...VALID_WIRE, startError: 42 }),
  },
  {
    name: 'negative integer',
    result: async () => mockResponse({ ...VALID_WIRE, readerWorkers: -1 }),
  },
  {
    name: 'fractional number',
    result: async () => mockResponse({ ...VALID_WIRE, senderWorkers: 0.5 }),
  },
  {
    name: 'unsafe integer',
    result: async () => mockResponse({
      ...VALID_WIRE,
      totalTransactions: Number.MAX_SAFE_INTEGER + 1,
    }),
  },
  {
    name: 'negative reader rate',
    result: async () => mockResponse({ ...VALID_WIRE, readerReadTps: -0.5 }),
  },
  {
    name: 'unsafe reader rows',
    result: async () => mockResponse({
      ...VALID_WIRE,
      readerRowsRead: Number.MAX_SAFE_INTEGER + 1,
    }),
  },
  {
    name: 'negative queue capacity',
    result: async () => mockResponse({ ...VALID_WIRE, queue1Capacity: -1 }),
  },
  {
    name: 'fractional queue depth',
    result: async () => mockResponse({ ...VALID_WIRE, queue1DepthBatches: 0.5 }),
  },
  {
    name: 'unsafe queued transactions',
    result: async () => mockResponse({
      ...VALID_WIRE,
      queue1QueuedTransactions: Number.MAX_SAFE_INTEGER + 1,
    }),
  },
  {
    name: 'empty reader source',
    result: async () => mockResponse({ ...VALID_WIRE, readerSource: '' }),
  },
  {
    name: 'wrong content type',
    result: async () => mockResponse(VALID_WIRE, { contentType: 'text/plain' }),
  },
  {
    name: 'missing content type',
    result: async () => mockResponse(VALID_WIRE, { contentType: null }),
  },
  {
    name: 'invalid JSON',
    result: async () => mockResponse(null, { jsonError: new SyntaxError() }),
  },
  {
    name: 'non-2xx response',
    result: async () => mockResponse(VALID_WIRE, { status: 503 }),
  },
  {
    name: 'network rejection',
    result: async () => { throw new TypeError('network unavailable') },
  },
]

describe('HttpAdapter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW_MS)
    fetchMock = vi.fn(pendingResponse)
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('starts immediately with the exact deep-frozen neutral snapshot', () => {
    const adapter = new HttpAdapter()
    const snapshot = adapter.getSnapshot()

    expect(adapter.kind).toBe('http')
    expect(snapshot).toEqual(expectedSnapshot(0, 'connecting'))
    expectDeepFrozen(snapshot)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(SNAPSHOT_ENDPOINT)

    const init = fetchMock.mock.calls[0]?.[1] as RequestInit
    expect(init).toMatchObject({
      method: 'GET',
      headers: { Accept: 'application/json' },
      signal: expect.any(AbortSignal),
    })
    expect(Object.keys(init).sort()).toEqual(['headers', 'method', 'signal'])
    expect(init.body).toBeUndefined()
    adapter.dispose()
  })

  it('maps only the twenty-four valid wire fields into a fresh frozen snapshot', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse(
      VALID_WIRE,
      { contentType: 'application/json; charset=utf-8' },
    ))
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    await flushPoll()
    const snapshot = adapter.getSnapshot()

    expect(snapshot).not.toBe(initial)
    expect(snapshot).toEqual(expectedSnapshot(1, 'connected', VALID_WIRE))
    expect(snapshot.reader.workers).toMatchObject({
      applied: 1,
      min: 1,
      max: 1,
      applyMode: 'unavailable',
    })
    expect(snapshot.reader).toMatchObject({
      readBatchSize: {
        applied: 50_000,
        min: 1_000,
        max: 100_000,
        step: 1_000,
        unit: 'transactions',
        applyMode: 'unavailable',
      },
      readTps: 3_500.5,
      rowsRead: 14_000,
      source: 'MBD-mini/trx/part/input.parquet',
    })
    expect(snapshot.sender.workers).toMatchObject({
      applied: 0,
      min: 0,
      max: 0,
      applyMode: 'unavailable',
    })
    expect(snapshot.queue1).toEqual({
      ...neutralQueue('reader-to-throttler', 'reader', 'throttler'),
      capacity: { ...control('batches', 8), min: 0, max: 8_192 },
      depthBatches: 6,
      queuedTransactions: 300_000,
      enqueuedBatchesTotal: 11,
      enqueuedTransactionsTotal: 550_000,
      dequeuedBatchesTotal: 5,
      dequeuedTransactionsTotal: 250_000,
      inputBatchesPerSecond: 2.5,
      inputTransactionsPerSecond: 125_000.5,
      outputBatchesPerSecond: 1.25,
      outputTransactionsPerSecond: 62_500.25,
      inputTps: 125_000.5,
      outputTps: 62_500.25,
      throughputTps: 62_500.25,
      blockedSenders: 1,
      oldestBlockedSenderMs: 450,
      blockedMs: 1_600,
    })
    expectDeepFrozen(snapshot)
    adapter.dispose()
  })

  it.each([0, 1, 2, 8_192])(
    'maps queue1 capacity %i to the discrete HTTP control range',
    async (queue1Capacity) => {
      const wire = { ...VALID_WIRE, queue1Capacity }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()

      expect(adapter.getSnapshot().queue1.capacity).toEqual({
        applied: queue1Capacity,
        preview: null,
        pending: null,
        min: 0,
        max: 8_192,
        step: 1,
        unit: 'batches',
        applyMode: 'unavailable',
      })
      adapter.dispose()
    },
  )

  it('maps reset reader metrics as zero values and no source', async () => {
    const resetWire: TestWireSnapshot = {
      ...VALID_WIRE,
      runState: 'idle',
      readerReadTps: 0,
      readerRowsRead: 0,
      readerSource: null,
    }
    fetchMock.mockResolvedValueOnce(mockResponse(resetWire))
    const adapter = new HttpAdapter()

    await flushPoll()

    expect(adapter.getSnapshot().reader).toMatchObject({
      readTps: 0,
      rowsRead: 0,
      source: null,
      state: 'idle',
    })
    adapter.dispose()
  })

  it.each(malformedCases)(
    'publishes one neutral error snapshot for $name',
    async ({ result }) => {
      fetchMock.mockImplementationOnce(result)
      const adapter = new HttpAdapter()
      const initial = adapter.getSnapshot()

      await flushPoll()
      const snapshot = adapter.getSnapshot()

      expect(snapshot).not.toBe(initial)
      expect(snapshot).toEqual(expectedSnapshot(1, 'error'))
      expectDeepFrozen(snapshot)
      expect(fetchMock).toHaveBeenCalledTimes(1)
      adapter.dispose()
    },
  )

  it('preserves last-known wire fields on failure and recovers on success', async () => {
    const recoveredWire: TestWireSnapshot = {
      ...VALID_WIRE,
      runState: 'paused',
      elapsedMs: 67_890,
      startError: 'previous start failed',
      totalTransactions: 84_000,
      readerWorkers: 2,
      senderWorkers: 3,
      readerReadTps: 2_000,
      readerReadBatchSize: 25_000,
      readerRowsRead: 28_000,
      readerSource: 'MBD-mini/trx/part/recovered.parquet',
      queue1Capacity: 16,
      queue1DepthBatches: 4,
      queue1QueuedTransactions: 100_000,
      queue1BlockedSenders: 0,
      queue1OldestBlockedSenderMs: 0,
      queue1BlockedMs: 2_000,
    }
    fetchMock
      .mockResolvedValueOnce(mockResponse(VALID_WIRE))
      .mockRejectedValueOnce(new TypeError('network unavailable'))
      .mockResolvedValueOnce(mockResponse(recoveredWire))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot())
      .toEqual(expectedSnapshot(1, 'connected', VALID_WIRE))

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(2, 'error', VALID_WIRE))

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot())
      .toEqual(expectedSnapshot(3, 'connected', recoveredWire))
    adapter.dispose()
  })

  it('polls immediately and on free one-second ticks without overlap', async () => {
    const first = deferred<Response>()
    fetchMock
      .mockReturnValueOnce(first.promise)
      .mockImplementation(pendingResponse)
    const adapter = new HttpAdapter()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(3_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)

    first.resolve(mockResponse(VALID_WIRE))
    await flushPoll()
    expect(adapter.getSnapshot().revision).toBe(1)

    await vi.advanceTimersByTimeAsync(999)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    adapter.dispose()
  })

  it('emits immediately and once per settled poll, then unsubscribes idempotently', async () => {
    const first = deferred<Response>()
    fetchMock
      .mockReturnValueOnce(first.promise)
      .mockRejectedValue(new TypeError('network unavailable'))
    const adapter = new HttpAdapter()
    const snapshots: LoadgenTelemetrySnapshot[] = []
    const unsubscribe = adapter.subscribe((snapshot) => snapshots.push(snapshot))

    expect(snapshots).toEqual([adapter.getSnapshot()])
    first.resolve(mockResponse(VALID_WIRE))
    await flushPoll()
    expect(snapshots).toHaveLength(2)
    expect(snapshots[1]?.revision).toBe(1)

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(snapshots).toHaveLength(3)
    expect(snapshots[2]?.revision).toBe(2)

    unsubscribe()
    unsubscribe()
    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot().revision).toBe(3)
    expect(snapshots).toHaveLength(3)
    adapter.dispose()
  })

  it.each(['resolve', 'reject'] as const)(
    'aborts and ignores a late $outcome after idempotent disposal',
    async (outcome) => {
      const request = deferred<Response>()
      fetchMock.mockReturnValueOnce(request.promise)
      const adapter = new HttpAdapter()
      const snapshots: LoadgenTelemetrySnapshot[] = []
      adapter.subscribe((snapshot) => snapshots.push(snapshot))
      const initial = adapter.getSnapshot()
      const init = fetchMock.mock.calls[0]?.[1] as RequestInit

      expect(vi.getTimerCount()).toBe(1)
      adapter.dispose()
      adapter.dispose()
      expect(vi.getTimerCount()).toBe(0)
      expect(init.signal?.aborted).toBe(true)

      if (outcome === 'resolve') {
        request.resolve(mockResponse(VALID_WIRE))
      } else {
        request.reject(new TypeError('late rejection'))
      }
      await flushPoll()

      expect(adapter.getSnapshot()).toBe(initial)
      expect(adapter.getSnapshot().revision).toBe(0)
      expect(snapshots).toEqual([initial])
      await vi.advanceTimersByTimeAsync(3_000)
      expect(fetchMock).toHaveBeenCalledTimes(1)
    },
  )

  it.each([
    { type: 'run' },
    { type: 'pause' },
    { type: 'reset' },
  ] as const)(
    'maps $type to the exact command POST request',
    async (command) => {
      fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
        ? Promise.resolve(mockCommandResponse())
        : Promise.resolve(mockResponse(VALID_WIRE)))
      const adapter = new HttpAdapter()

      await flushPoll()

      await adapter.dispatch(command)

      expect(snapshotFetchCalls()).toHaveLength(1)
      expect(commandFetchCalls()).toHaveLength(1)
      const [input, init] = commandFetchCalls()[0]!
      expect(input).toBe(COMMAND_ENDPOINT)
      expect(init).toEqual({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: command.type }),
      })
      expect(Object.keys(init as RequestInit).sort())
        .toEqual(['body', 'headers', 'method'])
      expect((init as RequestInit).signal).toBeUndefined()
      expect((init as RequestInit).headers).not.toHaveProperty('Accept')
      adapter.dispose()
    },
  )

  it('sends an idle read batch size through the command queue without updating the snapshot', async () => {
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, runState: 'idle' }
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockCommandResponse())
      : Promise.resolve(mockResponse(idleWire)))
    const adapter = new HttpAdapter()

    await flushPoll()
    const snapshot = adapter.getSnapshot()
    expect(snapshot.reader.readBatchSize).toEqual({
      applied: 50_000,
      preview: null,
      pending: null,
      min: 1_000,
      max: 100_000,
      step: 1_000,
      unit: 'transactions',
      applyMode: 'immediate',
    })

    const receipt = await adapter.dispatch({
      type: 'set-read-batch-size',
      value: 25_000,
    })

    expect(receipt).toMatchObject({
      accepted: true,
      commandType: 'set-read-batch-size',
      applyMode: 'immediate',
      snapshotRevision: 1,
      error: null,
    })
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"set-read-batch-size","value":25000}',
    })
    expect(adapter.getSnapshot()).toBe(snapshot)
    expect(adapter.getSnapshot().reader.readBatchSize.applied).toBe(50_000)
    adapter.dispose()
  })

  it.each([{ type: 'run' }, { type: 'pause' }] as const)(
    'does not dispatch $type from a stale snapshot after policy decoding fails',
    async (command) => {
      const invalidPolicyWire = {
        ...VALID_WIRE,
        policy: {
          ...VALID_WIRE.policy,
          readerReadBatchSize: {
            ...VALID_WIRE.policy.readerReadBatchSize,
            step: 0,
          },
        },
      }
      fetchMock
        .mockResolvedValueOnce(mockResponse(VALID_WIRE))
        .mockResolvedValueOnce(mockResponse(invalidPolicyWire))
      const adapter = new HttpAdapter()

      await flushPoll()
      await vi.advanceTimersByTimeAsync(1_000)
      await flushPoll()

      expect(adapter.getSnapshot()).toMatchObject({
        connectionState: 'error',
        policy: VALID_WIRE.policy,
      })
      const receipt = await adapter.dispatch(command)
      expect(receipt).toMatchObject({
        accepted: false,
        error: { code: 'unavailable', retryable: false },
      })
      expect(commandFetchCalls()).toHaveLength(0)
      adapter.dispose()
    },
  )

  it.each([
    { state: 'running' as const, value: 25_000 },
    { state: 'paused' as const, value: 25_000 },
    { state: 'idle' as const, value: 999 },
    { state: 'idle' as const, value: 1_001 },
    { state: 'idle' as const, value: 100_001 },
  ])(
    'rejects read batch size $value locally in $state',
    async ({ state, value }) => {
      const wire: TestWireSnapshot = { ...VALID_WIRE, runState: state }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()
      const receipt = await adapter.dispatch({
        type: 'set-read-batch-size',
        value,
      })

      expect(receipt).toMatchObject({
        accepted: false,
        applyMode: 'unavailable',
        error: { code: 'unavailable', retryable: false },
      })
      expect(commandFetchCalls()).toHaveLength(0)
      expect(adapter.getSnapshot().reader.readBatchSize).toMatchObject({
        applied: 50_000,
        applyMode: state === 'idle' ? 'immediate' : 'unavailable',
      })
      adapter.dispose()
    },
  )

  it('sends an idle queue1 capacity through the existing command queue', async () => {
    const idleWire: TestWireSnapshot = {
      ...VALID_WIRE,
      runState: 'idle',
      queue1Capacity: 2,
    }
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockCommandResponse())
      : Promise.resolve(mockResponse(idleWire)))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot().queue1.capacity).toEqual({
      applied: 2,
      preview: null,
      pending: null,
      min: 0,
      max: 8_192,
      step: 1,
      unit: 'batches',
      applyMode: 'immediate',
    })

    const receipt = await adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 8_192,
    })

    expect(receipt).toMatchObject({
      accepted: true,
      commandType: 'set-queue-capacity',
      applyMode: 'immediate',
      snapshotRevision: 1,
      error: null,
    })
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"set-queue-capacity","value":8192}',
    })
    adapter.dispose()
  })

  it.each([
    { state: 'running' as const, value: 2 },
    { state: 'paused' as const, value: 2 },
    { state: 'idle' as const, value: 3 },
  ])(
    'rejects queue1 capacity $value locally in $state',
    async ({ state, value }) => {
      const wire: TestWireSnapshot = { ...VALID_WIRE, runState: state }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()
      const receipt = await adapter.dispatch({
        type: 'set-queue-capacity',
        queue: 'reader-to-throttler',
        value,
      })

      expect(receipt).toMatchObject({
        accepted: false,
        applyMode: 'unavailable',
        error: { code: 'unavailable', retryable: false },
      })
      expect(commandFetchCalls()).toHaveLength(0)
      expect(adapter.getSnapshot().queue1.capacity).toMatchObject({
        applied: 8,
        applyMode: state === 'idle' ? 'immediate' : 'unavailable',
      })
      adapter.dispose()
    },
  )

  it('does not send a queued queue1 capacity after the snapshot enters Run', async () => {
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, runState: 'idle' }
    const runningWire: TestWireSnapshot = {
      ...VALID_WIRE,
      runState: 'running',
    }
    const runResponse = deferred<Response>()
    let snapshotRequests = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return runResponse.promise

      snapshotRequests += 1
      return Promise.resolve(mockResponse(
        snapshotRequests === 1 ? idleWire : runningWire,
      ))
    })
    const adapter = new HttpAdapter()

    await flushPoll()
    const runReceipt = adapter.dispatch({ type: 'run' })
    await Promise.resolve()
    const capacityReceipt = adapter.dispatch({
      type: 'set-queue-capacity',
      queue: 'reader-to-throttler',
      value: 4,
    })

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot().runState).toBe('running')

    runResponse.resolve(mockCommandResponse())
    await expect(runReceipt).resolves.toMatchObject({ accepted: true })
    await expect(capacityReceipt).resolves.toMatchObject({
      accepted: false,
      error: { code: 'unavailable', retryable: false },
    })
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it('accepts 204 without a response body and leaves snapshot authority intact', async () => {
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockCommandResponse(204))
      : pendingResponse())
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'run' })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'run',
      accepted: true,
      applyMode: 'immediate',
      appliedAtMs: NOW_MS,
      snapshotRevision: 0,
      error: null,
    })
    expectDeepFrozen(receipt)
    expect(adapter.getSnapshot()).toBe(initial)
    expect(adapter.getSnapshot().runState).toBe('idle')
    adapter.dispose()
  })

  it('accepts every 2xx status without reading headers or a malformed body', async () => {
    const { response, accessors } = responseWithThrowingRepresentation(299)
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(response)
      : pendingResponse())
    const adapter = new HttpAdapter()

    const receipt = await adapter.dispatch({ type: 'pause' })

    expect(receipt.accepted).toBe(true)
    expect(receipt.appliedAtMs).toBe(NOW_MS)
    Object.values(accessors).forEach((accessor) => {
      expect(accessor).not.toHaveBeenCalled()
    })
    adapter.dispose()
  })

  it('rejects every unsupported command locally while accounting for polling', async () => {
    const commands: readonly LoadgenCommand[] = [
      { type: 'set-requested-tps', value: 10_000 },
      { type: 'set-throttler-installation-mode', value: 'bypass' },
      { type: 'set-worker-count', actor: 'reader', value: 2 },
      { type: 'set-worker-count', actor: 'sender', value: 3 },
      { type: 'set-http-batch-size', value: 1_000 },
      { type: 'set-http-timeout', valueMs: 500 },
      { type: 'set-target-delay', valueMs: 40 },
      { type: 'set-target-error-rate', valuePercent: 2 },
    ]
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    for (const [index, command] of commands.entries()) {
      const receipt = await adapter.dispatch(command)
      expect(receipt).toEqual({
        commandId: `http-local-${index + 1}`,
        commandType: command.type,
        accepted: false,
        applyMode: 'unavailable',
        appliedAtMs: null,
        snapshotRevision: 0,
        error: {
          code: 'unavailable',
          message: 'command is not available in the HTTP adapter',
          retryable: false,
          details: null,
        },
      })
      expect(Object.isFrozen(receipt)).toBe(true)
      expect(Object.isFrozen(receipt.error)).toBe(true)
      expect(adapter.getSnapshot()).toBe(initial)
    }

    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(0)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    adapter.dispose()
  })

  it('maps an HTTP 422 start error into a nonretryable unavailable receipt', async () => {
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockResponse(
        { error: 'producer is unavailable' },
        { status: 422 },
      ))
      : pendingResponse())
    const adapter = new HttpAdapter()

    const receipt = await adapter.dispatch({ type: 'run' })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'run',
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: 0,
      error: {
        code: 'unavailable',
        message: 'producer is unavailable',
        retryable: false,
        details: null,
      },
    })
    expectDeepFrozen(receipt)
    adapter.dispose()
  })

  it.each([
    mockResponse({}, { status: 422 }),
    mockResponse({ error: '' }, { status: 422 }),
    mockResponse(null, { status: 422, jsonError: new SyntaxError() }),
  ])('keeps the HTTP status message for a malformed 422 error body', async (response) => {
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(response)
      : pendingResponse())
    const adapter = new HttpAdapter()

    const receipt = await adapter.dispatch({ type: 'run' })

    expect(receipt).toMatchObject({
      accepted: false,
      error: {
        code: 'unavailable',
        message: 'command request failed with HTTP status 422',
        retryable: false,
      },
    })
    adapter.dispose()
  })

  it.each([
    { status: 409, retryable: false },
    { status: 503, retryable: true },
  ])(
    'maps HTTP $status to an unavailable receipt with retryable=$retryable',
    async ({ status, retryable }) => {
      const { response, accessors } = responseWithThrowingRepresentation(status)
      fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
        ? Promise.resolve(response)
        : pendingResponse())
      const adapter = new HttpAdapter()
      const initial = adapter.getSnapshot()

      const receipt = await adapter.dispatch({ type: 'reset' })

      expect(receipt).toEqual({
        commandId: 'http-local-1',
        commandType: 'reset',
        accepted: false,
        applyMode: 'unavailable',
        appliedAtMs: null,
        snapshotRevision: 0,
        error: {
          code: 'unavailable',
          message: `command request failed with HTTP status ${status}`,
          retryable,
          details: null,
        },
      })
      expectDeepFrozen(receipt)
      expect(adapter.getSnapshot()).toBe(initial)
      expect(commandFetchCalls()).toHaveLength(1)
      Object.values(accessors).forEach((accessor) => {
        expect(accessor).not.toHaveBeenCalled()
      })
      adapter.dispose()
    },
  )

  it('maps a network failure without leaking details or retrying', async () => {
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.reject(new TypeError('secret transport detail'))
      : pendingResponse())
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'run' })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'run',
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: 0,
      error: {
        code: 'unavailable',
        message: 'command request failed due to a network error',
        retryable: true,
        details: null,
      },
    })
    expectDeepFrozen(receipt)
    expect(adapter.getSnapshot()).toBe(initial)
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it('returns disposed locally when dispatch starts after disposal', async () => {
    const adapter = new HttpAdapter()
    adapter.dispose()

    const receipt = await adapter.dispatch({ type: 'pause' })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'pause',
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: 0,
      error: {
        code: 'disposed',
        message: 'http adapter is disposed',
        retryable: false,
        details: null,
      },
    })
    expectDeepFrozen(receipt)
    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(0)
  })

  it('lets an in-flight POST settle but disposes queued commands without POST', async () => {
    const inFlight = deferred<Response>()
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? inFlight.promise
      : pendingResponse())
    const adapter = new HttpAdapter()
    const run = adapter.dispatch({ type: 'run' })
    await flushPoll()
    const pause = adapter.dispatch({ type: 'pause' })
    const reset = adapter.dispatch({ type: 'reset' })
    const pollInit = snapshotFetchCalls()[0]?.[1] as RequestInit
    const commandInit = commandFetchCalls()[0]?.[1] as RequestInit

    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
    expect(pollInit.signal?.aborted).toBe(true)
    expect(commandInit.signal).toBeUndefined()
    inFlight.resolve(mockCommandResponse(204))

    const [runReceipt, pauseReceipt, resetReceipt] = await Promise.all([
      run,
      pause,
      reset,
    ])
    expect(runReceipt).toMatchObject({
      commandId: 'http-local-1',
      accepted: true,
      error: null,
    })
    expect(pauseReceipt).toMatchObject({
      commandId: 'http-local-2',
      accepted: false,
      error: { code: 'disposed', retryable: false },
    })
    expect(resetReceipt).toMatchObject({
      commandId: 'http-local-3',
      accepted: false,
      error: { code: 'disposed', retryable: false },
    })
    expect(commandFetchCalls()).toHaveLength(1)
  })

  it('serializes lifecycle commands in FIFO order and continues after failure', async () => {
    const responses = [
      deferred<Response>(),
      deferred<Response>(),
      deferred<Response>(),
    ]
    let responseIndex = 0
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? responses[responseIndex++]!.promise
      : pendingResponse())
    const adapter = new HttpAdapter()

    const receipts = [
      adapter.dispatch({ type: 'run' }),
      adapter.dispatch({ type: 'pause' }),
      adapter.dispatch({ type: 'reset' }),
    ]
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"run"}',
    })

    responses[0].resolve(mockCommandResponse(204))
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(2)
    expect(commandFetchCalls()[1]?.[1]).toMatchObject({
      body: '{"action":"pause"}',
    })

    responses[1].resolve(mockCommandResponse(503))
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(3)
    expect(commandFetchCalls()[2]?.[1]).toMatchObject({
      body: '{"action":"reset"}',
    })

    responses[2].resolve(mockCommandResponse(204))
    const settled = await Promise.all(receipts)
    expect(settled.map(({ commandId }) => commandId)).toEqual([
      'http-local-1',
      'http-local-2',
      'http-local-3',
    ])
    expect(settled.map(({ accepted }) => accepted)).toEqual([true, false, true])
    expect(settled[1]?.error).toMatchObject({
      code: 'unavailable',
      retryable: true,
    })
    adapter.dispose()
  })

  it('runs polling beside POST while snapshots remain lifecycle authority', async () => {
    const firstPoll = deferred<Response>()
    const command = deferred<Response>()
    let pollCount = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return command.promise
      pollCount += 1
      return pollCount === 1 ? firstPoll.promise : pendingResponse()
    })
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()
    const receiptPromise = adapter.dispatch({ type: 'run' })
    await flushPoll()

    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(1)
    expect((commandFetchCalls()[0]![1] as RequestInit).signal).toBeUndefined()

    command.resolve(mockCommandResponse(204))
    const receipt = await receiptPromise
    expect(receipt.snapshotRevision).toBe(0)
    expect(adapter.getSnapshot()).toBe(initial)
    expect(adapter.getSnapshot().runState).toBe('idle')

    firstPoll.resolve(mockResponse(VALID_WIRE))
    await flushPoll()
    expect(adapter.getSnapshot()).not.toBe(initial)
    expect(adapter.getSnapshot()).toMatchObject({
      revision: 1,
      runState: 'running',
    })

    await vi.advanceTimersByTimeAsync(1_000)
    expect(snapshotFetchCalls()).toHaveLength(2)
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it('uses monotonic invocation IDs and the current revision at receipt creation', async () => {
    const poll = deferred<Response>()
    const firstCommand = deferred<Response>()
    let commandCount = 0
    fetchMock.mockImplementation((input) => {
      if (input === SNAPSHOT_ENDPOINT) return poll.promise
      commandCount += 1
      return commandCount === 1
        ? firstCommand.promise
        : Promise.resolve(mockCommandResponse(204))
    })
    const adapter = new HttpAdapter()
    const unsupported = await adapter.dispatch({
      type: 'set-requested-tps',
      value: 10_000,
    })
    const run = adapter.dispatch({ type: 'run' })
    const pause = adapter.dispatch({ type: 'pause' })
    await flushPoll()

    poll.resolve(mockResponse({ ...VALID_WIRE, runState: 'paused' }))
    await flushPoll()
    const authoritative = adapter.getSnapshot()
    expect(authoritative.revision).toBe(1)

    firstCommand.resolve(mockCommandResponse(204))
    const [runReceipt, pauseReceipt] = await Promise.all([run, pause])

    expect([unsupported, runReceipt, pauseReceipt].map(({ commandId }) => commandId))
      .toEqual(['http-local-1', 'http-local-2', 'http-local-3'])
    expect(runReceipt.snapshotRevision).toBe(1)
    expect(pauseReceipt.snapshotRevision).toBe(1)
    expect(adapter.getSnapshot()).toBe(authoritative)
    const allReceipts = [unsupported, runReceipt, pauseReceipt]
    allReceipts.forEach((receipt) => expectDeepFrozen(receipt))
    adapter.dispose()
  })
})
