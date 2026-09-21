import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type {
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  LoadgenPolicySnapshot,
  ChannelTelemetrySnapshot,
  RunState,
} from '../model/loadgen'
import { HttpAdapter } from './HttpAdapter'

interface TestWireSnapshot {
  readonly run: {
    readonly state: RunState
    readonly elapsedMs: number
    readonly startError: string | null
    readonly totalTransactions: number
  }
  readonly reader: {
    readonly workers: number
    readonly readTps: number
    readonly readBatchSize: number
    readonly rowsRead: number
    readonly source: string | null
  }
  readonly throttler: {
    readonly requestedTps: number
    readonly admittedTps: number
    readonly installationMode: 'installed' | 'bypass'
  }
  readonly sender: { readonly workers: number }
  readonly readerChannel: TestWireChannel
  readonly senderChannel: TestWireChannel
  readonly policy: LoadgenPolicySnapshot
}

interface TestWireChannel {
  readonly capacity: number
  readonly sentBatchesTotal: number
  readonly sentTransactionsTotal: number
  readonly receivedBatchesTotal: number
  readonly receivedTransactionsTotal: number
  readonly depthBatches: number
  readonly bufferedTransactions: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly blockedMs: number
  readonly inputBatchesPerSecond: number
  readonly inputTransactionsPerSecond: number
  readonly outputBatchesPerSecond: number
  readonly outputTransactionsPerSecond: number
}

interface MockResponseOptions {
  readonly status?: number
  readonly contentType?: string | null
  readonly jsonError?: Error
}

const VALID_WIRE: TestWireSnapshot = {
  run: { state: 'running', elapsedMs: 12_345, startError: null, totalTransactions: 42_000 },
  reader: { workers: 1, readTps: 3_500.5, readBatchSize: 50_000, rowsRead: 14_000, source: 'MBD-mini/trx/part/input.parquet' },
  throttler: { requestedTps: 200, admittedTps: 125_000.5, installationMode: 'installed' },
  sender: { workers: 0 },
  readerChannel: {
    capacity: 8, sentBatchesTotal: 11, sentTransactionsTotal: 550_000, receivedBatchesTotal: 5, receivedTransactionsTotal: 250_000,
    depthBatches: 6, bufferedTransactions: 300_000, blockedSenders: 1, oldestBlockedSenderMs: 450, blockedMs: 1_600,
    inputBatchesPerSecond: 2.5, inputTransactionsPerSecond: 125_000.5, outputBatchesPerSecond: 1.25, outputTransactionsPerSecond: 62_500.25,
  },
  senderChannel: {
    capacity: 0, sentBatchesTotal: 7, sentTransactionsTotal: 350_000, receivedBatchesTotal: 7, receivedTransactionsTotal: 350_000,
    depthBatches: 0, bufferedTransactions: 0, blockedSenders: 1, oldestBlockedSenderMs: 450, blockedMs: 1_600,
    inputBatchesPerSecond: 1.5, inputTransactionsPerSecond: 75_000.5, outputBatchesPerSecond: 1.25, outputTransactionsPerSecond: 62_500.25,
  },
  policy: {
    readerReadBatchSize: {
      default: 50_000,
      min: 1_000,
      max: 100_000,
      step: 1_000,
      unit: 'transactions',
      mutability: 'idle-only',
    },
    readerChannelCapacity: {
      default: 2,
      allowed: [0, 1, 2, 8, 16, 64, 8_192],
      unit: 'batches',
      mutability: 'idle-only',
    },
    senderChannelCapacity: {
      default: 0,
      allowed: [0, 1, 2, 8, 16, 64, 8_192],
      unit: 'batches',
      mutability: 'idle-only',
    },
    metricsWindowMs: {
      default: 1_000,
      min: 100,
      max: 10_000,
      step: 100,
      unit: 'milliseconds',
      mutability: 'startup-only',
    },
    throttlerRequestedTps: {
      default: 200,
      min: 0,
      max: 400,
      step: 25,
      unit: 'transactions/s',
      mutability: 'immediate',
    },
    throttlerInstallationMode: {
      default: 'installed',
      allowed: ['installed', 'bypass'],
      mutability: 'immediate',
    },
  },
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

function requestedTpsControl(
  value: number | null,
  policy: LoadgenPolicySnapshot['throttlerRequestedTps'] | null,
  connectionState: ConnectionState,
): NumericControlSnapshot {
  if (policy === null) return control('transactions/s')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: policy.min,
    max: policy.max,
    step: policy.step,
    unit: policy.unit,
    applyMode: connectionState === 'connected' ? 'immediate' : 'unavailable',
  }
}

function neutralChannel(
  id: ChannelTelemetrySnapshot['id'],
  from: ChannelTelemetrySnapshot['from'],
  to: ChannelTelemetrySnapshot['to'],
): ChannelTelemetrySnapshot {
  return {
    id,
    from,
    to,
    capacity: control('batches'),
    sentBatchesTotal: 0,
    sentTransactionsTotal: 0,
    receivedBatchesTotal: 0,
    receivedTransactionsTotal: 0,
    depthBatches: null,
    bufferedTransactions: null,
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

function readerChannel(
  wire: TestWireSnapshot | null,
  connectionState: ConnectionState,
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'reader-to-throttler',
    'reader',
    'throttler',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: {
      ...control('batches', wire.readerChannel.capacity),
      min: wire.policy.readerChannelCapacity.allowed[0]!,
      max: wire.policy.readerChannelCapacity.allowed.at(-1)!,
      applyMode: connectionState === 'connected' && wire.run.state === 'idle'
        ? 'immediate'
        : 'unavailable',
    },
    depthBatches: wire.readerChannel.depthBatches, bufferedTransactions: wire.readerChannel.bufferedTransactions,
    sentBatchesTotal: wire.readerChannel.sentBatchesTotal, sentTransactionsTotal: wire.readerChannel.sentTransactionsTotal,
    receivedBatchesTotal: wire.readerChannel.receivedBatchesTotal, receivedTransactionsTotal: wire.readerChannel.receivedTransactionsTotal,
    inputBatchesPerSecond: wire.readerChannel.inputBatchesPerSecond, inputTransactionsPerSecond: wire.readerChannel.inputTransactionsPerSecond,
    outputBatchesPerSecond: wire.readerChannel.outputBatchesPerSecond, outputTransactionsPerSecond: wire.readerChannel.outputTransactionsPerSecond,
    inputTps: wire.readerChannel.inputTransactionsPerSecond, outputTps: wire.readerChannel.outputTransactionsPerSecond,
    throughputTps: wire.readerChannel.outputTransactionsPerSecond, blockedSenders: wire.readerChannel.blockedSenders,
    oldestBlockedSenderMs: wire.readerChannel.oldestBlockedSenderMs, blockedMs: wire.readerChannel.blockedMs,
  }
}

function senderChannel(
  wire: TestWireSnapshot | null,
  connectionState: ConnectionState,
): ChannelTelemetrySnapshot {
  const channel = neutralChannel(
    'throttler-to-sender',
    'throttler',
    'sender',
  )
  if (wire === null) return channel

  return {
    ...channel,
    capacity: {
      ...control('batches', wire.senderChannel.capacity),
      min: wire.policy.senderChannelCapacity.allowed[0]!,
      max: wire.policy.senderChannelCapacity.allowed.at(-1)!,
      applyMode: connectionState === 'connected' && wire.run.state === 'idle'
        ? 'immediate'
        : 'unavailable',
    },
    depthBatches: wire.senderChannel.depthBatches, bufferedTransactions: wire.senderChannel.bufferedTransactions,
    sentBatchesTotal: wire.senderChannel.sentBatchesTotal, sentTransactionsTotal: wire.senderChannel.sentTransactionsTotal,
    receivedBatchesTotal: wire.senderChannel.receivedBatchesTotal, receivedTransactionsTotal: wire.senderChannel.receivedTransactionsTotal,
    inputBatchesPerSecond: wire.senderChannel.inputBatchesPerSecond, inputTransactionsPerSecond: wire.senderChannel.inputTransactionsPerSecond,
    outputBatchesPerSecond: wire.senderChannel.outputBatchesPerSecond, outputTransactionsPerSecond: wire.senderChannel.outputTransactionsPerSecond,
    inputTps: wire.senderChannel.inputTransactionsPerSecond, outputTps: wire.senderChannel.outputTransactionsPerSecond,
    throughputTps: wire.senderChannel.outputTransactionsPerSecond, blockedSenders: wire.senderChannel.blockedSenders,
    oldestBlockedSenderMs: wire.senderChannel.oldestBlockedSenderMs, blockedMs: wire.senderChannel.blockedMs,
  }
}

function expectedSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: TestWireSnapshot | null = null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.run.state ?? 'idle'

  return {
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.run.elapsedMs ?? 0,
    startError: wire?.run.startError ?? null,
    totalTransactions: wire?.run.totalTransactions ?? 0,
    policy: wire?.policy ?? null,
    reader: {
      id: 'reader',
      workers: control('workers', wire?.reader.workers ?? null),
      readBatchSize: readBatchSizeControl(
        wire?.reader.readBatchSize ?? null,
        wire?.policy.readerReadBatchSize ?? null,
        connectionState,
        runState,
      ),
      readTps: wire?.reader.readTps ?? null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: wire?.reader.rowsRead ?? null,
      source: wire?.reader.source ?? null,
      state: runState,
    },
    throttler: {
      id: 'throttler',
      requestedTps: requestedTpsControl(
        wire?.throttler.requestedTps ?? null,
        wire?.policy.throttlerRequestedTps ?? null,
        connectionState,
      ),
      installationMode: {
        applied: wire?.throttler.installationMode ?? null,
        pending: null,
        applyMode: connectionState === 'connected' && wire !== null
          ? 'immediate'
          : 'unavailable',
        writable: connectionState === 'connected' && wire !== null,
        unavailableReason: connectionState === 'connected' && wire !== null
          ? null
          : 'Недоступно в HTTP snapshot mode',
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: readerChannel(wire, connectionState),
    senderChannel: senderChannel(wire, connectionState),
    sender: {
      id: 'sender',
      workers: control('workers', wire?.sender.workers ?? null),
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
      run: { state: 'running', totalTransactions: 1 },
      reader: { workers: 1 },
    }),
  },
  {
    name: 'extra key',
    result: async () => mockResponse({ ...VALID_WIRE, extra: true }),
  },
  ...(['run', 'reader', 'throttler', 'sender', 'readerChannel', 'senderChannel'] as const).flatMap((section) => [
    {
      name: `missing ${section} section key`,
      result: async () => {
        const { [section]: _removed, ...withoutSection } = VALID_WIRE
        return mockResponse(withoutSection)
      },
    },
    {
      name: `extra ${section} section key`,
      result: async () => mockResponse({
        ...VALID_WIRE,
        [section]: { ...VALID_WIRE[section], extra: true },
      }),
    },
  ]),
  {
    name: 'missing policy',
    result: async () => {
      const { policy: _policy, ...withoutPolicy } = VALID_WIRE
      return mockResponse(withoutPolicy)
    },
  },
  {
    name: 'malformed readerChannel policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: {
        ...VALID_WIRE.policy,
        readerChannelCapacity: {
          ...VALID_WIRE.policy.readerChannelCapacity,
          allowed: [0, 2, 2],
        },
      },
    }),
  },
  {
    name: 'missing senderChannel policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: (() => {
        const { senderChannelCapacity: _senderChannel, ...policy } = VALID_WIRE.policy
        return policy
      })(),
    }),
  },
  {
    name: 'malformed senderChannel policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: {
        ...VALID_WIRE.policy,
        senderChannelCapacity: {
          ...VALID_WIRE.policy.senderChannelCapacity,
          allowed: [0, 2, 2],
        },
      },
    }),
  },
  {
    name: 'missing throttler requested TPS policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: (() => {
        const { throttlerRequestedTps: _requestedTps, ...policy } = VALID_WIRE.policy
        return policy
      })(),
    }),
  },
  {
    name: 'missing metrics window policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: (() => {
        const { metricsWindowMs: _metricsWindow, ...policy } = VALID_WIRE.policy
        return policy
      })(),
    }),
  },
  {
    name: 'malformed metrics window policy',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: {
        ...VALID_WIRE.policy,
        metricsWindowMs: {
          ...VALID_WIRE.policy.metricsWindowMs,
          mutability: 'immediate',
        },
      },
    }),
  },
  {
    name: 'unknown throttler installation mode policy field',
    result: async () => mockResponse({
      ...VALID_WIRE,
      policy: {
        ...VALID_WIRE.policy,
        throttlerInstallationMode: {
          ...VALID_WIRE.policy.throttlerInstallationMode,
          unknown: true,
        },
      },
    }),
  },
  {
    name: 'wrong-type requested TPS value',
    result: async () => mockResponse({ ...VALID_WIRE, throttler: { ...VALID_WIRE.throttler, requestedTps: '200' } }),
  },
  {
    name: 'missing throttler admitted TPS',
    result: async () => {
      const { admittedTps: _admittedTps, ...throttler } = VALID_WIRE.throttler
      return mockResponse({ ...VALID_WIRE, throttler })
    },
  },
  {
    name: 'wrong-type sender channel rate',
    result: async () => mockResponse({
      ...VALID_WIRE,
      senderChannel: { ...VALID_WIRE.senderChannel, inputTransactionsPerSecond: '75000.5' },
    }),
  },
  {
    name: 'sender channel capacity outside policy',
    result: async () => mockResponse({ ...VALID_WIRE, senderChannel: { ...VALID_WIRE.senderChannel, capacity: 3 } }),
  },
  {
    name: 'unknown applied installation mode',
    result: async () => mockResponse({
      ...VALID_WIRE,
      throttler: { ...VALID_WIRE.throttler, installationMode: 'removed' },
    }),
  },
  { name: 'null body', result: async () => mockResponse(null) },
  { name: 'array body', result: async () => mockResponse([VALID_WIRE]) },
  {
    name: 'invalid runState',
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'resetting' } }),
  },
  {
    name: 'numeric string',
    result: async () => mockResponse({
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, totalTransactions: '42000' },
    }),
  },
  {
    name: 'negative elapsed time',
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, elapsedMs: -1 } }),
  },
  {
    name: 'fractional elapsed time',
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, elapsedMs: 0.5 } }),
  },
  {
    name: 'unsafe elapsed time',
    result: async () => mockResponse({
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, elapsedMs: Number.MAX_SAFE_INTEGER + 1 },
    }),
  },
  {
    name: 'empty start error',
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, startError: '' } }),
  },
  {
    name: 'invalid start error',
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, startError: 42 } }),
  },
  {
    name: 'negative integer',
    result: async () => mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, workers: -1 } }),
  },
  {
    name: 'fractional number',
    result: async () => mockResponse({ ...VALID_WIRE, sender: { ...VALID_WIRE.sender, workers: 0.5 } }),
  },
  {
    name: 'unsafe integer',
    result: async () => mockResponse({
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, totalTransactions: Number.MAX_SAFE_INTEGER + 1 },
    }),
  },
  {
    name: 'negative reader rate',
    result: async () => mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, readTps: -0.5 } }),
  },
  {
    name: 'unsafe reader rows',
    result: async () => mockResponse({
      ...VALID_WIRE,
      reader: { ...VALID_WIRE.reader, rowsRead: Number.MAX_SAFE_INTEGER + 1 },
    }),
  },
  {
    name: 'negative channel capacity',
    result: async () => mockResponse({ ...VALID_WIRE, readerChannel: { ...VALID_WIRE.readerChannel, capacity: -1 } }),
  },
  {
    name: 'fractional channel depth',
    result: async () => mockResponse({ ...VALID_WIRE, readerChannel: { ...VALID_WIRE.readerChannel, depthBatches: 0.5 } }),
  },
  {
    name: 'unsafe buffered transactions',
    result: async () => mockResponse({
      ...VALID_WIRE,
      readerChannel: { ...VALID_WIRE.readerChannel, bufferedTransactions: Number.MAX_SAFE_INTEGER + 1 },
    }),
  },
  {
    name: 'empty reader source',
    result: async () => mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, source: '' } }),
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

  it('maps only the exact valid wire fields into a fresh frozen snapshot', async () => {
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
    expect(snapshot.throttler).toMatchObject({
      requestedTps: {
        applied: 200,
        min: 0,
        max: 400,
        step: 25,
        unit: 'transactions/s',
        applyMode: 'immediate',
      },
      admittedTps: 125_000.5,
      installationMode: {
        applied: 'installed',
        applyMode: 'immediate',
        writable: true,
        unavailableReason: null,
      },
    })
    expect(snapshot.readerChannel).toEqual({
      ...neutralChannel('reader-to-throttler', 'reader', 'throttler'),
      capacity: { ...control('batches', 8), min: 0, max: 8_192 },
      depthBatches: 6,
      bufferedTransactions: 300_000,
      sentBatchesTotal: 11,
      sentTransactionsTotal: 550_000,
      receivedBatchesTotal: 5,
      receivedTransactionsTotal: 250_000,
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
    expect(snapshot.senderChannel).toEqual({
      ...neutralChannel('throttler-to-sender', 'throttler', 'sender'),
      capacity: { ...control('batches', 0), min: 0, max: 8_192 },
      depthBatches: 0,
      bufferedTransactions: 0,
      sentBatchesTotal: 7,
      sentTransactionsTotal: 350_000,
      receivedBatchesTotal: 7,
      receivedTransactionsTotal: 350_000,
      inputBatchesPerSecond: 1.5,
      inputTransactionsPerSecond: 75_000.5,
      outputBatchesPerSecond: 1.25,
      outputTransactionsPerSecond: 62_500.25,
      inputTps: 75_000.5,
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
    'maps readerChannel capacity %i to the discrete HTTP control range',
    async (readerChannelCapacity) => {
      const wire = { ...VALID_WIRE, readerChannel: { ...VALID_WIRE.readerChannel, capacity: readerChannelCapacity } }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()

      expect(adapter.getSnapshot().readerChannel.capacity).toEqual({
        applied: readerChannelCapacity,
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
      run: { ...VALID_WIRE.run, state: 'idle' },
      reader: { ...VALID_WIRE.reader, readTps: 0, rowsRead: 0, source: null },
      throttler: { ...VALID_WIRE.throttler, admittedTps: 0 },
      senderChannel: { ...VALID_WIRE.senderChannel, sentBatchesTotal: 0, sentTransactionsTotal: 0, receivedBatchesTotal: 0, receivedTransactionsTotal: 0, blockedSenders: 0, oldestBlockedSenderMs: 0, blockedMs: 0, inputBatchesPerSecond: 0, inputTransactionsPerSecond: 0, outputBatchesPerSecond: 0, outputTransactionsPerSecond: 0 },
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
    expect(adapter.getSnapshot().throttler.admittedTps).toBe(0)
    expect(adapter.getSnapshot().senderChannel).toMatchObject({
      capacity: {
        ...control('batches', 0),
        min: 0,
        max: 8_192,
        applyMode: 'immediate',
      },
      depthBatches: 0,
      bufferedTransactions: 0,
      sentBatchesTotal: 0,
      sentTransactionsTotal: 0,
      receivedBatchesTotal: 0,
      receivedTransactionsTotal: 0,
      blockedSenders: 0,
      oldestBlockedSenderMs: 0,
      blockedMs: 0,
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 0,
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

  it('clears telemetry on failure and recovers on success', async () => {
    const recoveredWire: TestWireSnapshot = {
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, state: 'paused', elapsedMs: 67_890, startError: 'previous start failed', totalTransactions: 84_000 },
      reader: { ...VALID_WIRE.reader, workers: 2, readTps: 2_000, readBatchSize: 25_000, rowsRead: 28_000, source: 'MBD-mini/trx/part/recovered.parquet' },
      sender: { workers: 3 },
      readerChannel: { ...VALID_WIRE.readerChannel, capacity: 16, depthBatches: 4, bufferedTransactions: 100_000, blockedSenders: 0, oldestBlockedSenderMs: 0, blockedMs: 2_000 },
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
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(2, 'error'))

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

  it('sends an idle read batch size through the command channel without updating the snapshot', async () => {
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'idle' } }
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
        policy: null,
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
      const wire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state } }
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

  it('sends an idle readerChannel capacity through the existing command channel', async () => {
    const idleWire: TestWireSnapshot = {
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, state: 'idle' },
      readerChannel: { ...VALID_WIRE.readerChannel, capacity: 2 },
    }
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockCommandResponse())
      : Promise.resolve(mockResponse(idleWire)))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot().readerChannel.capacity).toEqual({
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
      type: 'set-reader-channel-capacity',
      value: 8_192,
    })

    expect(receipt).toMatchObject({
      accepted: true,
      commandType: 'set-reader-channel-capacity',
      applyMode: 'immediate',
      snapshotRevision: 1,
      error: null,
    })
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"set-reader-channel-capacity","value":8192}',
    })
    adapter.dispose()
  })

  it('sends an idle senderChannel capacity through the existing command channel', async () => {
    const idleWire: TestWireSnapshot = {
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, state: 'idle' },
      senderChannel: { ...VALID_WIRE.senderChannel, capacity: 0 },
    }
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? Promise.resolve(mockCommandResponse())
      : Promise.resolve(mockResponse(idleWire)))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot().senderChannel.capacity).toEqual({
      applied: 0,
      preview: null,
      pending: null,
      min: 0,
      max: 8_192,
      step: 1,
      unit: 'batches',
      applyMode: 'immediate',
    })

    const receipt = await adapter.dispatch({
      type: 'set-sender-channel-capacity',
      value: 8_192,
    })

    expect(receipt).toMatchObject({
      accepted: true,
      commandType: 'set-sender-channel-capacity',
      applyMode: 'immediate',
      snapshotRevision: 1,
      error: null,
    })
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"set-sender-channel-capacity","value":8192}',
    })
    expect(adapter.getSnapshot().senderChannel.capacity.applied).toBe(0)
    adapter.dispose()
  })

  it('serializes idle senderChannel capacity commands', async () => {
    const commandResponse = deferred<Response>()
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'idle' } }
    fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
      ? commandResponse.promise
      : Promise.resolve(mockResponse(idleWire)))
    const adapter = new HttpAdapter()

    await flushPoll()
    const first = adapter.dispatch({
      type: 'set-sender-channel-capacity',
      value: 1,
    })
    const second = adapter.dispatch({
      type: 'set-sender-channel-capacity',
      value: 2,
    })
    await Promise.resolve()
    expect(commandFetchCalls()).toHaveLength(1)

    commandResponse.resolve(mockCommandResponse())
    await first
    await Promise.resolve()
    expect(commandFetchCalls()).toHaveLength(2)
    await second
    expect(commandFetchCalls().map(([, init]) => (init as RequestInit).body))
      .toEqual([
        '{"action":"set-sender-channel-capacity","value":1}',
        '{"action":"set-sender-channel-capacity","value":2}',
      ])
    adapter.dispose()
  })

  it.each(['idle', 'running', 'paused'] as const)(
    'sends immediate throttler controls in $state and leaves applied state to snapshots',
    async (runState) => {
      const wire: TestWireSnapshot = {
        ...VALID_WIRE,
        run: { ...VALID_WIRE.run, state: runState },
        throttler: { ...VALID_WIRE.throttler, requestedTps: 0 },
      }
      fetchMock.mockImplementation((input) => input === COMMAND_ENDPOINT
        ? Promise.resolve(mockCommandResponse())
        : Promise.resolve(mockResponse(wire)))
      const adapter = new HttpAdapter()

      await flushPoll()
      expect(adapter.getSnapshot().throttler.requestedTps).toMatchObject({
        applied: 0,
        min: 0,
        max: 400,
        step: 25,
        applyMode: 'immediate',
      })

      const [tpsReceipt, modeReceipt] = await Promise.all([
        adapter.dispatch({ type: 'set-requested-tps', value: 400 }),
        adapter.dispatch({
          type: 'set-throttler-installation-mode',
          value: 'bypass',
        }),
      ])

      expect([tpsReceipt, modeReceipt].map(({ accepted }) => accepted))
        .toEqual([true, true])
      expect(commandFetchCalls().map(([, init]) =>
        (init as RequestInit).body,
      )).toEqual([
        '{"action":"set-requested-tps","value":400}',
        '{"action":"set-throttler-installation-mode","value":"bypass"}',
      ])
      expect(adapter.getSnapshot().throttler).toMatchObject({
        requestedTps: { applied: 0 },
        installationMode: { applied: 'installed' },
      })
      adapter.dispose()
    },
  )

  it.each([
    { type: 'set-requested-tps', value: 25 } as const,
    { type: 'set-throttler-installation-mode', value: 'bypass' } as const,
  ])('does not send buffered $type after snapshot invalidation', async (command) => {
    const firstCommand = deferred<Response>()
    let snapshotRequests = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return firstCommand.promise
      snapshotRequests += 1
      return Promise.resolve(mockResponse(
        snapshotRequests === 1
          ? { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'running' } }
          : { ...VALID_WIRE, throttler: { ...VALID_WIRE.throttler, requestedTps: '25' } },
      ))
    })
    const adapter = new HttpAdapter()

    await flushPoll()
    const first = adapter.dispatch({ type: 'set-requested-tps', value: 25 })
    await Promise.resolve()
    const buffered = adapter.dispatch(command)
    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    firstCommand.resolve(mockCommandResponse())

    await expect(first).resolves.toMatchObject({ accepted: true })
    await expect(buffered).resolves.toMatchObject({
      accepted: false,
      error: { code: 'unavailable', retryable: false },
    })
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it.each([
    { state: 'running' as const, value: 2 },
    { state: 'paused' as const, value: 2 },
    { state: 'idle' as const, value: 3 },
  ])(
    'rejects readerChannel capacity $value locally in $state',
    async ({ state, value }) => {
      const wire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state } }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()
      const receipt = await adapter.dispatch({
        type: 'set-reader-channel-capacity',
        value,
      })

      expect(receipt).toMatchObject({
        accepted: false,
        applyMode: 'unavailable',
        error: { code: 'unavailable', retryable: false },
      })
      expect(commandFetchCalls()).toHaveLength(0)
      expect(adapter.getSnapshot().readerChannel.capacity).toMatchObject({
        applied: 8,
        applyMode: state === 'idle' ? 'immediate' : 'unavailable',
      })
      adapter.dispose()
    },
  )

  it('rejects senderChannel capacity while the adapter has no connected snapshot', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse({
      ...VALID_WIRE,
      policy: (() => {
        const { senderChannelCapacity: _senderChannel, ...policy } = VALID_WIRE.policy
        return policy
      })(),
    }))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot().connectionState).toBe('error')
    const receipt = await adapter.dispatch({
      type: 'set-sender-channel-capacity',
      value: 1,
    })

    expect(receipt).toMatchObject({
      accepted: false,
      error: { code: 'unavailable', retryable: false },
    })
    expect(commandFetchCalls()).toHaveLength(0)
    adapter.dispose()
  })

  it('does not send a buffered readerChannel capacity after the snapshot enters Run', async () => {
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'idle' } }
    const runningWire: TestWireSnapshot = {
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, state: 'running' },
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
      type: 'set-reader-channel-capacity',
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

  it.each([
    { state: 'running' as const, value: 2 },
    { state: 'paused' as const, value: 2 },
    { state: 'idle' as const, value: 3 },
  ])(
    'rejects senderChannel capacity $value locally in $state',
    async ({ state, value }) => {
      const wire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state } }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()
      const receipt = await adapter.dispatch({
        type: 'set-sender-channel-capacity',
        value,
      })

      expect(receipt).toMatchObject({
        accepted: false,
        applyMode: 'unavailable',
        error: { code: 'unavailable', retryable: false },
      })
      expect(commandFetchCalls()).toHaveLength(0)
      expect(adapter.getSnapshot().senderChannel.capacity).toMatchObject({
        applied: 0,
        applyMode: state === 'idle' ? 'immediate' : 'unavailable',
      })
      adapter.dispose()
    },
  )

  it('does not send a buffered senderChannel capacity after the snapshot enters Pause', async () => {
    const idleWire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'idle' } }
    const pausedWire: TestWireSnapshot = { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'paused' } }
    const pauseResponse = deferred<Response>()
    let snapshotRequests = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return pauseResponse.promise

      snapshotRequests += 1
      return Promise.resolve(mockResponse(
        snapshotRequests === 1 ? idleWire : pausedWire,
      ))
    })
    const adapter = new HttpAdapter()

    await flushPoll()
    const pauseReceipt = adapter.dispatch({ type: 'pause' })
    await Promise.resolve()
    const capacityReceipt = adapter.dispatch({
      type: 'set-sender-channel-capacity',
      value: 4,
    })

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot().runState).toBe('paused')

    pauseResponse.resolve(mockCommandResponse())
    await expect(pauseReceipt).resolves.toMatchObject({ accepted: true })
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

  it('lets an in-flight POST settle but disposes buffered commands without POST', async () => {
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

    poll.resolve(mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'paused' } }))
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
