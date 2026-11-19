import { readFileSync } from 'node:fs'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'
import type {
  ConnectionState,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
  NumericControlSnapshot,
  LoadgenConfigSnapshot,
  ChannelTelemetrySnapshot,
  RunState,
} from '../model/loadgen'
import { HttpAdapter } from './HttpAdapter'

interface TestWireSnapshot {
  readonly run: {
    readonly state: RunState
    readonly elapsedMs: number
    readonly totalTransactions: number
  }
  readonly reader: {
    readonly workers: number
    readonly liveWorkers: number
    readonly idleWorkers: number
    readonly readingWorkers: number
    readonly blockedWorkers: number
    readonly drainingWorkers: number
    readonly drainingIdleWorkers: number
    readonly drainingReadingWorkers: number
    readonly drainingBlockedWorkers: number
    readonly readTps: number
    readonly readBatchSize: number
    readonly rowsRead: number
    readonly sourceDirectory: string
    readonly sourceError: {
      readonly category: string
      readonly operation: string
      readonly relativePath: string
      readonly message: string
    } | null
  }
  readonly throttler: {
    readonly requestedTps: number
    readonly admittedTps: number
    readonly installed: boolean
  }
  readonly sender: {
    readonly workers: number
    readonly liveWorkers: number
    readonly idleWorkers: number
    readonly inFlightWorkers: number
    readonly backoffWorkers: number
    readonly drainingWorkers: number
    readonly drainingIdleWorkers: number
    readonly drainingInFlightWorkers: number
    readonly drainingBackoffWorkers: number
  }
  readonly readerChannel: TestWireChannel
  readonly senderChannel: TestWireChannel
  readonly config: LoadgenConfigSnapshot
}

interface TestWireChannel {
  readonly capacity: number
  readonly depthBatches: number
  readonly bufferedTransactions: number
  readonly blockedSenders: number
  readonly oldestBlockedSenderMs: number
  readonly blockedMs: number
  readonly inputTransactionsPerSecond: number
  readonly outputTransactionsPerSecond: number
}

interface MockResponseOptions {
  readonly status?: number
  readonly contentType?: string | null
  readonly jsonError?: Error
}

const VALID_WIRE: TestWireSnapshot = {
  run: { state: 'running', elapsedMs: 12_345, totalTransactions: 42_000 },
  reader: {
    workers: 1,
    liveWorkers: 1,
    idleWorkers: 0,
    readingWorkers: 1,
    blockedWorkers: 0,
    drainingWorkers: 0,
    drainingIdleWorkers: 0,
    drainingReadingWorkers: 0,
    drainingBlockedWorkers: 0,
    readTps: 3_500.5,
    readBatchSize: 50_000,
    rowsRead: 14_000,
    sourceDirectory: 'C:/dataset',
    sourceError: null,
  },
  throttler: { requestedTps: 200, admittedTps: 125_000.5, installed: true },
  sender: {
    workers: 32,
    liveWorkers: 0,
    idleWorkers: 0,
    inFlightWorkers: 0,
    backoffWorkers: 0,
    drainingWorkers: 0,
    drainingIdleWorkers: 0,
    drainingInFlightWorkers: 0,
    drainingBackoffWorkers: 0,
  },
  readerChannel: {
    capacity: 8,
    depthBatches: 6,
    bufferedTransactions: 300_000,
    blockedSenders: 1,
    oldestBlockedSenderMs: 450,
    blockedMs: 1_600,
    inputTransactionsPerSecond: 125_000.5,
    outputTransactionsPerSecond: 62_500.25,
  },
  senderChannel: {
    capacity: 0,
    depthBatches: 0,
    bufferedTransactions: 0,
    blockedSenders: 1,
    oldestBlockedSenderMs: 450,
    blockedMs: 1_600,
    inputTransactionsPerSecond: 75_000.5,
    outputTransactionsPerSecond: 62_500.25,
  },
  config: {
    readerWorkers: { initial: 1, max: 7 },
    senderWorkers: { initial: 32, max: 32 },
    throttlerRequestedTps: { initial: 200, max: 400, step: 25 },
  },
}

function withRunState(state: RunState): TestWireSnapshot {
  return {
    ...VALID_WIRE,
    run: { ...VALID_WIRE.run, state },
    reader:
      state === 'starting'
        ? {
            ...VALID_WIRE.reader,
            liveWorkers: 0,
            idleWorkers: 0,
            readingWorkers: 0,
            blockedWorkers: 0,
            drainingWorkers: 0,
            drainingIdleWorkers: 0,
            drainingReadingWorkers: 0,
            drainingBlockedWorkers: 0,
          }
        : VALID_WIRE.reader,
    sender:
      state === 'stopping'
        ? {
            ...VALID_WIRE.sender,
            liveWorkers: 32,
            idleWorkers: 28,
            inFlightWorkers: 3,
            backoffWorkers: 1,
            drainingWorkers: 3,
            drainingIdleWorkers: 1,
            drainingInFlightWorkers: 1,
            drainingBackoffWorkers: 1,
          }
        : VALID_WIRE.sender,
  }
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

function mockResponse(body: unknown, options: MockResponseOptions = {}): Response {
  const headers = new Headers()
  const contentType = options.contentType === undefined ? 'application/json' : options.contentType
  if (contentType !== null) headers.set('Content-Type', contentType)

  const json =
    options.jsonError === undefined
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
  const headers = vi.fn(() => {
    throw new Error('headers must be ignored')
  })
  const body = vi.fn(() => {
    throw new Error('body must be ignored')
  })
  const json = vi.fn(() => {
    throw new Error('json must be ignored')
  })
  const text = vi.fn(() => {
    throw new Error('text must be ignored')
  })
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

function control(unit: string, applied: number | null = null): NumericControlSnapshot {
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

function requestedTpsControl(
  value: number | null,
  config: LoadgenConfigSnapshot['throttlerRequestedTps'] | null,
  writable: boolean,
): NumericControlSnapshot {
  if (config === null) return control('transactions/s')

  return {
    applied: value,
    preview: null,
    pending: null,
    min: 0,
    max: config.max,
    step: config.step,
    unit: 'transactions/s',
    applyMode: writable ? 'immediate' : 'unavailable',
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
    capacity: null,
    depthBatches: null,
    bufferedTransactions: null,
    handoffBatches: 0,
    handoffBatchesTotal: 0,
    blockedSenders: 0,
    oldestBlockedSenderMs: 0,
    inputTransactionsPerSecond: 0,
    outputTransactionsPerSecond: 0,
    inputTps: null,
    outputTps: null,
    throughputTps: null,
    blockedMs: null,
    trend: 'unknown',
  }
}

function readerChannel(wire: TestWireSnapshot | null): ChannelTelemetrySnapshot {
  const channel = neutralChannel('reader-to-throttler', 'reader', 'throttler')
  if (wire === null) return channel

  return {
    ...channel,
    capacity: wire.readerChannel.capacity,
    depthBatches: wire.readerChannel.depthBatches,
    bufferedTransactions: wire.readerChannel.bufferedTransactions,
    inputTransactionsPerSecond: wire.readerChannel.inputTransactionsPerSecond,
    outputTransactionsPerSecond: wire.readerChannel.outputTransactionsPerSecond,
    inputTps: wire.readerChannel.inputTransactionsPerSecond,
    outputTps: wire.readerChannel.outputTransactionsPerSecond,
    throughputTps: wire.readerChannel.outputTransactionsPerSecond,
    blockedSenders: wire.readerChannel.blockedSenders,
    oldestBlockedSenderMs: wire.readerChannel.oldestBlockedSenderMs,
    blockedMs: wire.readerChannel.blockedMs,
  }
}

function senderChannel(wire: TestWireSnapshot | null): ChannelTelemetrySnapshot {
  const channel = neutralChannel('throttler-to-sender', 'throttler', 'sender')
  if (wire === null) return channel

  return {
    ...channel,
    capacity: wire.senderChannel.capacity,
    depthBatches: wire.senderChannel.depthBatches,
    bufferedTransactions: wire.senderChannel.bufferedTransactions,
    inputTransactionsPerSecond: wire.senderChannel.inputTransactionsPerSecond,
    outputTransactionsPerSecond: wire.senderChannel.outputTransactionsPerSecond,
    inputTps: wire.senderChannel.inputTransactionsPerSecond,
    outputTps: wire.senderChannel.outputTransactionsPerSecond,
    throughputTps: wire.senderChannel.outputTransactionsPerSecond,
    blockedSenders: wire.senderChannel.blockedSenders,
    oldestBlockedSenderMs: wire.senderChannel.oldestBlockedSenderMs,
    blockedMs: wire.senderChannel.blockedMs,
  }
}

function expectedSnapshot(
  revision: number,
  connectionState: ConnectionState,
  wire: TestWireSnapshot | null = null,
): LoadgenTelemetrySnapshot {
  const runState = wire?.run.state ?? 'idle'
  const writable = connectionState === 'connected' && runState === 'running'

  return {
    revision,
    adapterKind: 'http',
    connectionState,
    runState,
    elapsedMs: wire?.run.elapsedMs ?? 0,
    totalTransactions: wire?.run.totalTransactions ?? 0,
    config: wire?.config ?? null,
    reader: {
      id: 'reader',
      workers:
        wire === null
          ? control('workers', null)
          : {
              applied: wire.reader.workers,
              preview: null,
              pending: null,
              min: 1,
              max: wire.config.readerWorkers!.max,
              step: 1,
              unit: 'workers',
              applyMode: writable ? 'immediate' : 'unavailable',
            },
      liveWorkers: wire?.reader.liveWorkers ?? 0,
      drainingWorkers: wire?.reader.drainingWorkers ?? 0,
      idleWorkers: wire?.reader.idleWorkers ?? 0,
      readingWorkers: wire?.reader.readingWorkers ?? 0,
      blockedWorkers: wire?.reader.blockedWorkers ?? 0,
      drainingIdleWorkers: wire?.reader.drainingIdleWorkers ?? 0,
      drainingReadingWorkers: wire?.reader.drainingReadingWorkers ?? 0,
      drainingBlockedWorkers: wire?.reader.drainingBlockedWorkers ?? 0,
      readBatchSize: wire?.reader.readBatchSize ?? null,
      readTps: wire?.reader.readTps ?? null,
      configuredCapacityTps: null,
      limitationReason: null,
      rowsRead: wire?.reader.rowsRead ?? null,
      sourceDirectory: wire?.reader.sourceDirectory,
      sourceError: wire?.reader.sourceError ?? null,
      state: runState,
    },
    throttler: {
      id: 'throttler',
      requestedTps: requestedTpsControl(
        wire?.throttler.requestedTps ?? null,
        wire?.config.throttlerRequestedTps ?? null,
        writable,
      ),
      installed: {
        applied: wire?.throttler.installed ?? null,
        pending: null,
        applyMode: writable ? 'immediate' : 'unavailable',
        writable,
        unavailableReason: writable ? null : 'Unavailable in HTTP mode',
      },
      admittedTps: wire?.throttler.admittedTps ?? null,
      limitedMs: null,
      state: runState,
    },
    readerChannel: readerChannel(wire),
    senderChannel: senderChannel(wire),
    sender: {
      id: 'sender',
      workers: wire
        ? {
            ...control('workers', wire.sender.workers),
            min: 1,
            max: 32,
            step: 1,
            applyMode: writable ? 'immediate' : 'unavailable',
          }
        : control('workers'),
      liveWorkers: wire?.sender.liveWorkers ?? 0,
      drainingWorkers: wire?.sender.drainingWorkers ?? 0,
      idleWorkers: wire?.sender.idleWorkers ?? 0,
      inFlightWorkers: wire?.sender.inFlightWorkers ?? 0,
      backoffWorkers: wire?.sender.backoffWorkers ?? 0,
      drainingIdleWorkers: wire?.sender.drainingIdleWorkers ?? 0,
      drainingInFlightWorkers: wire?.sender.drainingInFlightWorkers ?? 0,
      drainingBackoffWorkers: wire?.sender.drainingBackoffWorkers ?? 0,
      timeoutMs: null,
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
    name: 'negative Reader aggregate count',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        reader: { ...VALID_WIRE.reader, readingWorkers: -1 },
      }),
  },
  {
    name: 'missing key',
    result: async () =>
      mockResponse({
        run: { state: 'running', totalTransactions: 1 },
        reader: { workers: 1 },
      }),
  },
  ...(['run', 'reader', 'throttler', 'sender', 'readerChannel', 'senderChannel'] as const).map(
    (section) => ({
      name: `missing ${section} section key`,
      result: async () => {
        const { [section]: _removed, ...withoutSection } = VALID_WIRE
        return mockResponse(withoutSection)
      },
    }),
  ),
  {
    name: 'missing config',
    result: async () => {
      const { config: _config, ...withoutConfig } = VALID_WIRE
      return mockResponse(withoutConfig)
    },
  },

  {
    name: 'missing throttler requested TPS config',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        config: (() => {
          const { throttlerRequestedTps: _requestedTps, ...config } = VALID_WIRE.config
          return config
        })(),
      }),
  },
  ...(['senderWorkers'] as const).flatMap((field) => [
    {
      name: `missing config ${field}`,
      result: async () => {
        const config = { ...VALID_WIRE.config } as Record<string, unknown>
        delete config[field]
        return mockResponse({ ...VALID_WIRE, config })
      },
    },
    {
      name: `wrong-type config ${field}`,
      result: async () =>
        mockResponse({
          ...VALID_WIRE,
          config: { ...VALID_WIRE.config, [field]: null },
        }),
    },
  ]),
  {
    name: 'Sender config with an initial value above max',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        config: {
          ...VALID_WIRE.config,
          senderWorkers: { ...VALID_WIRE.config.senderWorkers, initial: 33 },
        },
      }),
  },
  ...[
    { initial: 0, max: 400, step: 0 },
    { initial: 0, max: 0, step: 25 },
    { initial: 425, max: 400, step: 25 },
    { initial: 201, max: 400, step: 25 },
  ].map((throttlerRequestedTps) => ({
    name: `invalid TPS config ${JSON.stringify(throttlerRequestedTps)}`,
    result: async () =>
      mockResponse({ ...VALID_WIRE, config: { ...VALID_WIRE.config, throttlerRequestedTps } }),
  })),
  {
    name: 'aggregate sender count has wrong type',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        sender: { ...VALID_WIRE.sender, inFlightWorkers: 'one' },
      }),
  },
  ...([{ idleWorkers: -1 }, { inFlightWorkers: 0.5 }, { backoffWorkers: 'one' }] as const).map(
    (sender, index) => ({
      name: `invalid stopping Sender field ${index}`,
      result: async () => {
        const wire = withRunState('stopping')
        return mockResponse({ ...wire, sender: { ...wire.sender, ...sender } })
      },
    }),
  ),
  {
    name: 'wrong-type requested TPS value',
    result: async () =>
      mockResponse({ ...VALID_WIRE, throttler: { ...VALID_WIRE.throttler, requestedTps: '200' } }),
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
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        senderChannel: { ...VALID_WIRE.senderChannel, inputTransactionsPerSecond: '75000.5' },
      }),
  },
  {
    name: 'negative sender channel capacity',
    result: async () =>
      mockResponse({ ...VALID_WIRE, senderChannel: { ...VALID_WIRE.senderChannel, capacity: -1 } }),
  },
  {
    name: 'unknown applied installation mode',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        throttler: { ...VALID_WIRE.throttler, installed: 'removed' },
      }),
  },
  ...([null, 0, 'false', 'installed', 'bypass'] as const).map((installed) => ({
    name: `invalid installed type ${installed}`,
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        throttler: { ...VALID_WIRE.throttler, installed },
      }),
  })),
  {
    name: 'legacy throttler field',
    result: async () => {
      const { installed: _installed, ...throttler } = VALID_WIRE.throttler
      return mockResponse({
        ...VALID_WIRE,
        throttler: { ...throttler, installationMode: 'installed' },
      })
    },
  },
  {
    name: 'missing installed',
    result: async () => {
      const { installed: _installed, ...throttler } = VALID_WIRE.throttler
      return mockResponse({ ...VALID_WIRE, throttler })
    },
  },
  ...(['readerWorkers', 'senderWorkers', 'throttlerRequestedTps'] as const).flatMap((field) => [
    {
      name: `legacy metadata in ${field}`,
      result: async () =>
        mockResponse({
          ...VALID_WIRE,
          config: { ...VALID_WIRE.config, [field]: { ...VALID_WIRE.config[field], min: 0 } },
        }),
    },
    {
      name: `unsafe max in ${field}`,
      result: async () =>
        mockResponse({
          ...VALID_WIRE,
          config: {
            ...VALID_WIRE.config,
            [field]: { ...VALID_WIRE.config[field], max: Number.MAX_SAFE_INTEGER + 1 },
          },
        }),
    },
  ]),
  {
    name: 'legacy config branch',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        config: {
          ...VALID_WIRE.config,
          throttlerInstalled: { initial: true, allowed: [true, false] },
        },
      }),
  },
  {
    name: 'zero batch size',
    result: async () =>
      mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, readBatchSize: 0 } }),
  },
  { name: 'null body', result: async () => mockResponse(null) },
  { name: 'array body', result: async () => mockResponse([VALID_WIRE]) },
  ...['idle', 'paused', 'resetting'].map((state) => ({
    name: 'obsolete run state ' + state,
    result: async () => mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, state } }),
  })),
  {
    name: 'numeric string',
    result: async () =>
      mockResponse({
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
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        run: { ...VALID_WIRE.run, elapsedMs: Number.MAX_SAFE_INTEGER + 1 },
      }),
  },
  ...(['category', 'operation', 'relativePath', 'message'] as const).flatMap((field) => [
    {
      name: `missing Reader source error ${field}`,
      result: async () => {
        const sourceError: Record<string, unknown> = {
          category: 'source',
          operation: 'read',
          relativePath: 'broken.parquet',
          message: 'error',
        }
        delete sourceError[field]
        return mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, sourceError } })
      },
    },
    {
      name: `wrong-type Reader source error ${field}`,
      result: async () =>
        mockResponse({
          ...VALID_WIRE,
          reader: {
            ...VALID_WIRE.reader,
            sourceError: {
              category: 'source',
              operation: 'read',
              relativePath: 'broken.parquet',
              message: 'error',
              [field]: null,
            },
          },
        }),
    },
  ]),
  {
    name: 'negative integer',
    result: async () =>
      mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, workers: -1 } }),
  },
  {
    name: 'fractional number',
    result: async () =>
      mockResponse({ ...VALID_WIRE, sender: { ...VALID_WIRE.sender, workers: 0.5 } }),
  },
  {
    name: 'unsafe integer',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        run: { ...VALID_WIRE.run, totalTransactions: Number.MAX_SAFE_INTEGER + 1 },
      }),
  },
  {
    name: 'negative reader rate',
    result: async () =>
      mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, readTps: -0.5 } }),
  },
  ...[NaN, Infinity].map((readTps) => ({
    name: `non-finite reader rate ${readTps}`,
    result: async () => mockResponse({ ...VALID_WIRE, reader: { ...VALID_WIRE.reader, readTps } }),
  })),
  {
    name: 'unsafe reader rows',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        reader: { ...VALID_WIRE.reader, rowsRead: Number.MAX_SAFE_INTEGER + 1 },
      }),
  },
  {
    name: 'negative channel capacity',
    result: async () =>
      mockResponse({ ...VALID_WIRE, readerChannel: { ...VALID_WIRE.readerChannel, capacity: -1 } }),
  },
  {
    name: 'fractional channel depth',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        readerChannel: { ...VALID_WIRE.readerChannel, depthBatches: 0.5 },
      }),
  },
  {
    name: 'unsafe buffered transactions',
    result: async () =>
      mockResponse({
        ...VALID_WIRE,
        readerChannel: {
          ...VALID_WIRE.readerChannel,
          bufferedTransactions: Number.MAX_SAFE_INTEGER + 1,
        },
      }),
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
    result: async () => {
      throw new TypeError('network unavailable')
    },
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
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it.each(['readerWorkers', 'throttlerRequestedTps', 'senderWorkers'] as const)(
    'requires initial in %s and treats a legacy alias as an unknown field',
    async (field) => {
      vi.spyOn(console, 'warn').mockImplementation(() => undefined)
      const valid = JSON.parse(JSON.stringify(VALID_WIRE))
      for (const shape of ['initial-only', 'legacy-only', 'both', 'missing']) {
        const wire = structuredClone(valid)
        if (shape === 'legacy-only' || shape === 'both')
          wire.config[field].default = wire.config[field].initial
        if (shape === 'legacy-only' || shape === 'missing') delete wire.config[field].initial
        fetchMock
          .mockResolvedValueOnce(mockResponse(valid))
          .mockResolvedValueOnce(mockResponse(wire))
        const adapter = new HttpAdapter()
        await flushPoll()
        expect(adapter.getSnapshot().connectionState).toBe('connected')
        await vi.advanceTimersByTimeAsync(1_000)
        await flushPoll()
        if (shape === 'initial-only') {
          expect(adapter.getSnapshot().connectionState).toBe('connected')
          expect(adapter.getSnapshot().config).toEqual(valid.config)
        } else {
          expect(adapter.getSnapshot()).toMatchObject({
            connectionState: 'error',
            config: valid.config,
          })
          const receipt = await adapter.dispatch({ type: 'set-requested-tps', value: 200 })
          expect(receipt).toMatchObject({ accepted: false, error: { code: 'unavailable' } })
          expect(commandFetchCalls()).toHaveLength(0)
        }
        adapter.dispose()
      }
    },
  )

  it('accepts the config public contract', async () => {
    const { config: settings, ...telemetry } = VALID_WIRE
    fetchMock.mockResolvedValueOnce(mockResponse({ ...telemetry, config: settings }))
    const adapter = new HttpAdapter()
    await flushPoll()
    expect(adapter.getSnapshot().connectionState).toBe('connected')
    expect(adapter.getSnapshot()).toMatchObject({ config: settings })
    adapter.dispose()
  })

  it.each(['starting', 'running', 'stopping', 'faulted'] as const)(
    'decodes the complete Go fixture with independent measurements in %s',
    async (state) => {
      const wire = JSON.parse(readFileSync('../loadgen/testdata/snapshot.json', 'utf8'))
      wire.run.state = state
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()
      await flushPoll()

      expect(adapter.getSnapshot()).toMatchObject({
        connectionState: 'connected',
        runState: state,
        elapsedMs: 102,
        totalTransactions: 101,
        config: wire.config,
        reader: {
          workers: { applied: 11 },
          liveWorkers: 12,
          readingWorkers: 14,
          drainingWorkers: 16,
          sourceError: wire.reader.sourceError,
        },
        sender: { workers: { applied: 41 }, liveWorkers: 42, drainingWorkers: 46 },
        throttler: { requestedTps: { applied: 31 } },
        readerChannel: { inputTransactionsPerSecond: 62.5, outputTransactionsPerSecond: 64.5 },
        senderChannel: { inputTransactionsPerSecond: 82.5, outputTransactionsPerSecond: 84.5 },
      })
      expect(warn).not.toHaveBeenCalled()
      fetchMock.mockResolvedValue(mockCommandResponse())
      for (const command of [
        { type: 'set-reader-workers', value: 11 },
        { type: 'set-sender-workers', value: 41 },
        { type: 'set-requested-tps', value: 31 },
      ] as const) {
        expect((await adapter.dispatch(command)).accepted).toBe(false)
      }
      expect(commandFetchCalls()).toHaveLength(0)
      if (state === 'running') {
        expect((await adapter.dispatch({ type: 'set-reader-workers', value: 7 })).accepted).toBe(
          true,
        )
        expect(commandFetchCalls()).toHaveLength(1)
      }
      adapter.dispose()
    },
  )

  it('accepts source error strings without imposing category, operation or path conventions', async () => {
    const sourceError = {
      category: '',
      operation: 'future-operation',
      relativePath: '',
      message: '',
    }
    fetchMock.mockResolvedValueOnce(
      mockResponse({
        ...VALID_WIRE,
        reader: { ...VALID_WIRE.reader, sourceDirectory: '', sourceError },
      }),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    expect(adapter.getSnapshot()).toMatchObject({
      connectionState: 'connected',
      reader: { sourceDirectory: '', sourceError },
    })
    adapter.dispose()
  })

  it('uses the Sender worker range from the snapshot', async () => {
    const senderWorkers = { initial: 5, max: 35 }
    fetchMock.mockResolvedValueOnce(
      mockResponse({
        ...VALID_WIRE,
        config: { ...VALID_WIRE.config, senderWorkers },
        sender: { ...VALID_WIRE.sender, workers: 35 },
      }),
    )
    const adapter = new HttpAdapter()
    await flushPoll()

    expect(adapter.getSnapshot()).toMatchObject({
      connectionState: 'connected',
      config: { senderWorkers },
      sender: { workers: { applied: 35, min: 1, max: 35, step: 1, applyMode: 'immediate' } },
    })
    adapter.dispose()
  })

  it.each(['legacy-only', 'missing', 'null', 'invalid'] as const)(
    'rejects %s config contract and blocks stale dispatch',
    async (shape) => {
      const { config: settings, ...telemetry } = VALID_WIRE
      const valid = { ...telemetry, config: settings }
      const invalid =
        shape === 'legacy-only'
          ? { ...telemetry, ['policy']: settings }
          : shape === 'missing'
            ? telemetry
            : { ...telemetry, config: shape === 'null' ? null : [] }
      fetchMock
        .mockResolvedValueOnce(mockResponse(valid))
        .mockResolvedValueOnce(mockResponse(invalid))
      const adapter = new HttpAdapter()
      await flushPoll()
      expect(adapter.getSnapshot().connectionState).toBe('connected')
      await vi.advanceTimersByTimeAsync(1_000)
      await flushPoll()
      expect(adapter.getSnapshot()).toMatchObject({ connectionState: 'error', config: settings })
      const receipt = await adapter.dispatch({ type: 'set-requested-tps', value: 200 })
      expect(receipt).toMatchObject({ accepted: false, error: { code: 'unavailable' } })
      expect(commandFetchCalls()).toHaveLength(0)
      adapter.dispose()
    },
  )

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
    fetchMock.mockResolvedValueOnce(
      mockResponse(VALID_WIRE, { contentType: 'application/json; charset=utf-8' }),
    )
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    await flushPoll()
    const snapshot = adapter.getSnapshot()

    expect(snapshot).not.toBe(initial)
    expect(snapshot).toEqual(expectedSnapshot(1, 'connected', VALID_WIRE))
    expect(snapshot.reader.workers).toMatchObject({
      applied: 1,
      min: 1,
      max: 7,
      applyMode: 'immediate',
    })
    expect(snapshot.reader).toMatchObject({
      readBatchSize: VALID_WIRE.reader.readBatchSize,
      readTps: 3_500.5,
      rowsRead: 14_000,
    })
    expect(snapshot.sender.workers).toMatchObject({
      applied: 32,
      min: 1,
      max: 32,
      applyMode: 'immediate',
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
      installed: {
        applied: true,
        applyMode: 'immediate',
        writable: true,
        unavailableReason: null,
      },
    })
    expect(snapshot.readerChannel).toEqual({
      ...neutralChannel('reader-to-throttler', 'reader', 'throttler'),
      capacity: 8,
      depthBatches: 6,
      bufferedTransactions: 300_000,
      inputTransactionsPerSecond: 125_000.5,
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
      capacity: 0,
      depthBatches: 0,
      bufferedTransactions: 0,
      inputTransactionsPerSecond: 75_000.5,
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
    'maps readerChannel capacity %i to read-only telemetry',
    async (readerChannelCapacity) => {
      const wire = {
        ...VALID_WIRE,
        readerChannel: { ...VALID_WIRE.readerChannel, capacity: readerChannelCapacity },
      }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()

      expect(adapter.getSnapshot().readerChannel.capacity).toEqual(readerChannelCapacity)
      adapter.dispose()
    },
  )

  it('maps startup reader metrics as zero values', async () => {
    const startingWire: TestWireSnapshot = {
      ...withRunState('starting'),
      reader: {
        ...VALID_WIRE.reader,
        liveWorkers: 0,
        idleWorkers: 0,
        readingWorkers: 0,
        blockedWorkers: 0,
        drainingWorkers: 0,
        drainingIdleWorkers: 0,
        drainingReadingWorkers: 0,
        drainingBlockedWorkers: 0,
        readTps: 0,
        rowsRead: 0,
      },
      throttler: { ...VALID_WIRE.throttler, admittedTps: 0 },
      senderChannel: {
        ...VALID_WIRE.senderChannel,
        blockedSenders: 0,
        oldestBlockedSenderMs: 0,
        blockedMs: 0,
        inputTransactionsPerSecond: 0,
        outputTransactionsPerSecond: 0,
      },
    }
    fetchMock.mockResolvedValueOnce(mockResponse(startingWire))
    const adapter = new HttpAdapter()

    await flushPoll()

    expect(adapter.getSnapshot().reader).toMatchObject({
      readTps: 0,
      rowsRead: 0,
      state: 'starting',
    })
    expect(adapter.getSnapshot().throttler.admittedTps).toBe(0)
    expect(adapter.getSnapshot().senderChannel).toMatchObject({
      capacity: 0,
      depthBatches: 0,
      bufferedTransactions: 0,
      blockedSenders: 0,
      oldestBlockedSenderMs: 0,
      blockedMs: 0,
      inputTransactionsPerSecond: 0,
      outputTransactionsPerSecond: 0,
    })
    adapter.dispose()
  })

  it.each(malformedCases)('publishes one neutral error snapshot for $name', async ({ result }) => {
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
  })

  it.each([
    { path: 'extra', wire: { ...VALID_WIRE, extra: true } },
    { path: 'policy', wire: { ...VALID_WIRE, policy: VALID_WIRE.config } },
    ...(['run', 'reader', 'throttler', 'sender', 'readerChannel', 'senderChannel'] as const).map(
      (section) => ({
        path: `${section}.extra`,
        wire: { ...VALID_WIRE, [section]: { ...VALID_WIRE[section], extra: true } },
      }),
    ),
  ])('accepts and strips $path with a warning', async ({ path, wire }) => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    fetchMock.mockResolvedValueOnce(mockResponse(wire))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(1, 'connected', VALID_WIRE))
    expect(warn).toHaveBeenCalledExactlyOnceWith(expect.stringContaining(path))
    adapter.dispose()
  })

  it('warns once per set of unknown paths regardless of key order, independently per adapter', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const obsoleteDelay = ['sender', 'Simulated', 'DelayMs'].join('')
    const obsoleteErrorRate = ['sender', 'Simulated', 'ErrorRatePercent'].join('')
    const obsoleteFields = { [obsoleteDelay]: { initial: 10 }, [obsoleteErrorRate]: { initial: 2 } }
    const first = {
      ...VALID_WIRE,
      reader: { ...VALID_WIRE.reader, ...obsoleteFields },
      extra: true,
    }
    const reordered = {
      extra: false,
      ...VALID_WIRE,
      reader: { [obsoleteErrorRate]: null, ...VALID_WIRE.reader, [obsoleteDelay]: null },
    }
    const sourceError = {
      category: 'source',
      operation: 'read',
      relativePath: 'broken.parquet',
      message: 'Could not read file',
    } as const
    const second = {
      ...VALID_WIRE,
      reader: { ...VALID_WIRE.reader, sourceError: { ...sourceError, 'future.path': true } },
    }
    fetchMock
      .mockResolvedValueOnce(mockResponse(first))
      .mockResolvedValueOnce(mockResponse(reordered))
      .mockResolvedValueOnce(mockResponse(second))
      .mockResolvedValueOnce(mockResponse(first))
      .mockResolvedValueOnce(mockResponse(first))
    const adapter = new HttpAdapter()

    await flushPoll()
    const firstWarning = expect.stringContaining(
      `extra, reader.${obsoleteDelay}, reader.${obsoleteErrorRate}`,
    )
    expect(warn).toHaveBeenCalledExactlyOnceWith(firstWarning)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(warn).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(warn).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenLastCalledWith(
      expect.stringContaining('reader.sourceError["future.path"]'),
    )
    expect(adapter.getSnapshot().reader.sourceError).toEqual(sourceError)
    expectDeepFrozen(adapter.getSnapshot().reader.sourceError)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(warn).toHaveBeenCalledTimes(2)

    const other = new HttpAdapter()
    await flushPoll()
    expect(warn).toHaveBeenCalledTimes(3)
    expect(warn).toHaveBeenLastCalledWith(firstWarning)
    other.dispose()
    adapter.dispose()
  })

  it.each([
    { status: 503, contentType: 'application/json' },
    { status: 200, contentType: 'text/plain' },
  ])('releases an unread GET with status $status and $contentType', async (options) => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const response = mockResponse(null, options)
    fetchMock.mockResolvedValueOnce(response)
    const adapter = new HttpAdapter()

    await flushPoll()
    const init = snapshotFetchCalls()[0]?.[1] as RequestInit
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(1, 'error'))
    expect(response.json).not.toHaveBeenCalled()
    expect(init.signal?.aborted).toBe(true)
    adapter.dispose()
  })

  it('retains decoded telemetry on failure, disables controls and recovers on success', async () => {
    const recoveredWire: TestWireSnapshot = {
      ...VALID_WIRE,
      run: { ...VALID_WIRE.run, state: 'running', elapsedMs: 67_890, totalTransactions: 84_000 },
      reader: {
        ...VALID_WIRE.reader,
        workers: 2,
        liveWorkers: 2,
        idleWorkers: 1,
        readingWorkers: 1,
        readTps: 2_000,
        readBatchSize: 25_000,
        rowsRead: 28_000,
      },
      sender: {
        ...VALID_WIRE.sender,
        workers: 3,
        liveWorkers: 3,
        idleWorkers: 1,
        inFlightWorkers: 1,
        backoffWorkers: 1,
      },
      readerChannel: {
        ...VALID_WIRE.readerChannel,
        capacity: 16,
        depthBatches: 4,
        bufferedTransactions: 100_000,
        blockedSenders: 0,
        oldestBlockedSenderMs: 0,
        blockedMs: 2_000,
      },
    }
    fetchMock
      .mockResolvedValueOnce(mockResponse(VALID_WIRE))
      .mockRejectedValueOnce(new TypeError('network unavailable'))
      .mockResolvedValueOnce(mockResponse(recoveredWire))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(1, 'connected', VALID_WIRE))

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    const stale = adapter.getSnapshot()
    expect(stale).toEqual(expectedSnapshot(2, 'error', VALID_WIRE))
    expectDeepFrozen(stale)
    const derived = new ChannelFlowStateDeriver().derive(stale, Date.now())
    expect(derived.readerChannel.flowState).toBe('connection-error')
    expect(derived.senderChannel.flowState).toBe('connection-error')
    await expect(adapter.dispatch({ type: 'set-requested-tps', value: 25 })).resolves.toMatchObject(
      { accepted: false, error: { code: 'unavailable', retryable: false } },
    )
    expect(commandFetchCalls()).toHaveLength(0)

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(3, 'connected', recoveredWire))
    adapter.dispose()
  })

  it('warns on changed decoding messages and clears the deduplication after recovery', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const invalidRun = mockResponse({ ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'idle' } })
    const invalidJson = mockResponse(null, { jsonError: new SyntaxError('bad snapshot JSON') })
    fetchMock
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(mockResponse(null, { status: 503 }))
      .mockResolvedValueOnce(invalidJson)
      .mockResolvedValueOnce(invalidJson)
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(mockResponse(VALID_WIRE))
      .mockResolvedValueOnce(invalidRun)
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenLastCalledWith(expect.stringContaining('snapshot run'))
    await vi.advanceTimersByTimeAsync(2_000)
    expect(warn).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2_000)
    expect(warn).toHaveBeenCalledTimes(2)
    expect(warn).toHaveBeenLastCalledWith(expect.stringContaining('bad snapshot JSON'))
    await vi.advanceTimersByTimeAsync(1_000)
    expect(warn).toHaveBeenCalledTimes(3)
    expect(warn).toHaveBeenLastCalledWith(expect.stringContaining('snapshot run'))
    await vi.advanceTimersByTimeAsync(2_000)
    expect(warn).toHaveBeenCalledTimes(4)
    expect(warn).toHaveBeenLastCalledWith(expect.stringContaining('snapshot run'))
    adapter.dispose()
  })

  it.each(['starting', 'stopping', 'faulted'] as const)(
    'disables live changes in %s and preserves observed values',
    async (state) => {
      const wire = withRunState(state)
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()
      await flushPoll()

      const snapshot = adapter.getSnapshot()
      expect(snapshot).toEqual(expectedSnapshot(1, 'connected', wire))
      expect(snapshot.reader.workers.applyMode).toBe('unavailable')
      expect(snapshot.sender.workers.applyMode).toBe('unavailable')
      expect(snapshot.throttler.requestedTps.applyMode).toBe('unavailable')
      expect(snapshot.throttler.installed.writable).toBe(false)
      await expect(
        adapter.dispatch({ type: 'set-reader-workers', value: 2 }),
      ).resolves.toMatchObject({ accepted: false })
      expect(commandFetchCalls()).toHaveLength(0)
      adapter.dispose()
    },
  )

  it('polls immediately and on free one-second ticks without overlap', async () => {
    const first = deferred<Response>()
    fetchMock.mockReturnValueOnce(first.promise).mockImplementation(pendingResponse)
    const adapter = new HttpAdapter()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
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

  it.each(['headers', 'body'] as const)('times out a GET stalled at the %s', async (stage) => {
    const stalled = deferred<never>()
    const headers = deferred<Response>()
    const recovery = deferred<Response>()
    const json = vi.fn(() => stalled.promise)
    fetchMock
      .mockImplementationOnce((_input, init: RequestInit) => {
        init.signal?.addEventListener('abort', () => stalled.reject(init.signal?.reason), {
          once: true,
        })
        return stage === 'headers' ? stalled.promise : headers.promise
      })
      .mockReturnValueOnce(recovery.promise)
    const adapter = new HttpAdapter()
    const init = snapshotFetchCalls()[0]?.[1] as RequestInit
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)

    await vi.advanceTimersByTimeAsync(1_000)
    if (stage === 'body') headers.resolve({ ...mockResponse(null), json })
    await vi.advanceTimersByTimeAsync(999)
    expect(adapter.getSnapshot().connectionState).toBe('connecting')
    expect(init.signal?.aborted).toBe(false)
    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(json).toHaveBeenCalledTimes(stage === 'body' ? 1 : 0)

    await vi.advanceTimersByTimeAsync(1)
    expect(init.signal?.aborted).toBe(true)
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(1, 'error'))
    expect(warn).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1_000)
    expect(snapshotFetchCalls()).toHaveLength(2)
    recovery.resolve(mockResponse(VALID_WIRE))
    await flushPoll()
    expect(adapter.getSnapshot()).toEqual(expectedSnapshot(2, 'connected', VALID_WIRE))
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

  it.each(['running'] as const)(
    'sends exact immediate Sender commands in %s and reads applied values from the next snapshot',
    async (runState) => {
      const firstWire = withRunState(runState)
      const appliedWire: TestWireSnapshot = {
        ...firstWire,
        sender: {
          ...firstWire.sender,
          workers: 7,
        },
      }
      let polls = 0
      fetchMock.mockImplementation((input) =>
        input === COMMAND_ENDPOINT
          ? Promise.resolve(mockCommandResponse())
          : Promise.resolve(mockResponse(++polls === 1 ? firstWire : appliedWire)),
      )
      const adapter = new HttpAdapter()
      await flushPoll()

      for (const command of [{ type: 'set-sender-workers', value: 7 }] as const) {
        await expect(adapter.dispatch(command)).resolves.toMatchObject({
          accepted: true,
          applyMode: 'immediate',
        })
      }
      expect(commandFetchCalls().map(([, init]) => (init as RequestInit).body)).toEqual([
        '{"action":"set-sender-workers","value":7}',
      ])
      await vi.advanceTimersByTimeAsync(1_000)
      await flushPoll()
      expect(adapter.getSnapshot().sender.workers.applied).toBe(7)
      adapter.dispose()
    },
  )

  it.each(['idleWorkers', 'readingWorkers', 'blockedWorkers'] as const)(
    'accepts aggregate Reader count %s',
    async (field) => {
      const wire = {
        ...VALID_WIRE,
        reader: {
          ...VALID_WIRE.reader,
          liveWorkers: 1,
          idleWorkers: 0,
          readingWorkers: 0,
          blockedWorkers: 0,
          [field]: 1,
        },
      }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()
      await flushPoll()
      expect(adapter.getSnapshot().reader[field]).toBe(1)
      adapter.dispose()
    },
  )

  it.each(['running'] as const)(
    'sends exact Reader worker commands in %s and reads each applied value from the next snapshot',
    async (runState) => {
      const firstWire = withRunState(runState)
      const oneReaderWire: TestWireSnapshot = {
        ...firstWire,
        reader: { ...firstWire.reader, workers: 1 },
      }
      const sevenReaderWire: TestWireSnapshot = {
        ...oneReaderWire,
        reader: { ...oneReaderWire.reader, workers: 7 },
      }
      let polls = 0
      fetchMock.mockImplementation((input) =>
        input === COMMAND_ENDPOINT
          ? Promise.resolve(mockCommandResponse())
          : Promise.resolve(
              mockResponse([firstWire, oneReaderWire, sevenReaderWire][Math.min(polls++, 2)]!),
            ),
      )
      const adapter = new HttpAdapter()
      await flushPoll()

      for (const value of [1, 7]) {
        await expect(
          adapter.dispatch({
            type: 'set-reader-workers',
            value,
          }),
        ).resolves.toMatchObject({
          accepted: true,
          applyMode: 'immediate',
        })
        await vi.advanceTimersByTimeAsync(1_000)
        await flushPoll()
        expect(adapter.getSnapshot().reader.workers.applied).toBe(value)
      }
      expect(commandFetchCalls().map(([, init]) => (init as RequestInit).body)).toEqual([
        '{"action":"set-reader-workers","value":1}',
        '{"action":"set-reader-workers","value":7}',
      ])
      adapter.dispose()
    },
  )

  it.each([true, false])(
    'sends immediate throttler installed=%s and confirms only by poll',
    async (installed) => {
      let wire: TestWireSnapshot = {
        ...VALID_WIRE,
        throttler: { ...VALID_WIRE.throttler, requestedTps: 0, installed: !installed },
      }
      fetchMock.mockImplementation((input) =>
        input === COMMAND_ENDPOINT
          ? Promise.resolve(mockCommandResponse())
          : Promise.resolve(mockResponse(wire)),
      )
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
          type: 'set-throttler-installed',
          value: installed,
        }),
      ])

      expect([tpsReceipt, modeReceipt].map(({ accepted }) => accepted)).toEqual([true, true])
      expect(commandFetchCalls().map(([, init]) => (init as RequestInit).body)).toEqual([
        '{"action":"set-requested-tps","value":400}',
        JSON.stringify({ action: 'set-throttler-installed', value: installed }),
      ])
      expect(adapter.getSnapshot().throttler).toMatchObject({
        requestedTps: { applied: 0 },
        installed: { applied: !installed },
      })
      wire = { ...wire, throttler: { ...wire.throttler, requestedTps: 400, installed } }
      await vi.advanceTimersByTimeAsync(1_000)
      await flushPoll()
      expect(adapter.getSnapshot().throttler).toMatchObject({
        requestedTps: { applied: 400 },
        installed: { applied: installed },
      })
      adapter.dispose()
    },
  )

  it.each([
    { type: 'set-requested-tps', value: 25 } as const,
    { type: 'set-throttler-installed', value: false } as const,
  ])('does not send buffered $type after snapshot invalidation', async (command) => {
    const firstCommand = deferred<Response>()
    let snapshotRequests = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return firstCommand.promise
      snapshotRequests += 1
      return Promise.resolve(
        mockResponse(
          snapshotRequests === 1
            ? { ...VALID_WIRE, run: { ...VALID_WIRE.run, state: 'running' } }
            : { ...VALID_WIRE, throttler: { ...VALID_WIRE.throttler, requestedTps: '25' } },
        ),
      )
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

  it('accepts 204 without a response body and leaves snapshot authority intact', async () => {
    fetchMock.mockImplementation((input) =>
      input === COMMAND_ENDPOINT
        ? Promise.resolve(mockCommandResponse(204))
        : Promise.resolve(mockResponse(VALID_WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'set-sender-workers', value: 7 })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'set-sender-workers',
      accepted: true,
      applyMode: 'immediate',
      appliedAtMs: NOW_MS,
      snapshotRevision: 1,
      error: null,
    })
    expectDeepFrozen(receipt)
    expect(adapter.getSnapshot()).toBe(initial)
    expect(adapter.getSnapshot().runState).toBe('running')
    adapter.dispose()
  })

  it('accepts 299 without reading headers or a malformed body', async () => {
    const { response, accessors } = responseWithThrowingRepresentation(299)
    fetchMock.mockImplementation((input) =>
      input === COMMAND_ENDPOINT
        ? Promise.resolve(response)
        : Promise.resolve(mockResponse(VALID_WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()

    const receipt = await adapter.dispatch({ type: 'set-sender-workers', value: 7 })

    expect(receipt.accepted).toBe(true)
    expect(receipt.appliedAtMs).toBe(NOW_MS)
    const init = commandFetchCalls()[0]?.[1] as RequestInit
    expect(init.signal?.aborted).toBe(true)
    Object.values(accessors).forEach((accessor) => {
      expect(accessor).not.toHaveBeenCalled()
    })
    adapter.dispose()
  })

  it('rejects an unknown command locally while accounting for polling', async () => {
    const command = { type: 'unknown' } as unknown as LoadgenCommand
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch(command)
    expect(receipt).toEqual({
      commandId: 'http-local-1',
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
    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(0)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    adapter.dispose()
  })

  it.each([
    { status: 409, retryable: false },
    { status: 503, retryable: true },
  ])(
    'maps HTTP $status to an unavailable receipt with retryable=$retryable',
    async ({ status, retryable }) => {
      const { response, accessors } = responseWithThrowingRepresentation(status)
      fetchMock.mockImplementation((input) =>
        input === COMMAND_ENDPOINT
          ? Promise.resolve(response)
          : Promise.resolve(mockResponse(VALID_WIRE)),
      )
      const adapter = new HttpAdapter()
      await flushPoll()
      const initial = adapter.getSnapshot()

      const receipt = await adapter.dispatch({ type: 'set-throttler-installed', value: false })

      expect(receipt).toEqual({
        commandId: 'http-local-1',
        commandType: 'set-throttler-installed',
        accepted: false,
        applyMode: 'unavailable',
        appliedAtMs: null,
        snapshotRevision: 1,
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
      const init = commandFetchCalls()[0]?.[1] as RequestInit
      expect(init.signal?.aborted).toBe(true)
      Object.values(accessors).forEach((accessor) => {
        expect(accessor).not.toHaveBeenCalled()
      })
      adapter.dispose()
    },
  )

  it('maps a network failure without leaking details or retrying', async () => {
    fetchMock.mockImplementation((input) =>
      input === COMMAND_ENDPOINT
        ? Promise.reject(new TypeError('secret transport detail'))
        : Promise.resolve(mockResponse(VALID_WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'set-requested-tps', value: 25 })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'set-requested-tps',
      accepted: false,
      applyMode: 'unavailable',
      appliedAtMs: null,
      snapshotRevision: 1,
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

    const receipt = await adapter.dispatch({ type: 'set-sender-workers', value: 7 })

    expect(receipt).toEqual({
      commandId: 'http-local-1',
      commandType: 'set-sender-workers',
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

  it('aborts the active POST and disposes buffered commands without POST', async () => {
    const inFlight = deferred<Response>()
    fetchMock.mockImplementation((input, init: RequestInit) => {
      if (input !== COMMAND_ENDPOINT) return Promise.resolve(mockResponse(VALID_WIRE))
      init.signal?.addEventListener('abort', () => inFlight.reject(init.signal?.reason), {
        once: true,
      })
      return inFlight.promise
    })
    const adapter = new HttpAdapter()
    await flushPoll()
    const first = adapter.dispatch({ type: 'set-requested-tps', value: 25 })
    await flushPoll()
    const second = adapter.dispatch({ type: 'set-sender-workers', value: 7 })
    const third = adapter.dispatch({ type: 'set-throttler-installed', value: false })
    const commandInit = commandFetchCalls()[0]?.[1] as RequestInit

    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
    expect(commandInit.signal?.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)

    const [firstReceipt, secondReceipt, thirdReceipt] = await Promise.all([first, second, third])
    expect(firstReceipt).toMatchObject({
      commandId: 'http-local-1',
      accepted: false,
      error: { code: 'disposed', retryable: false },
    })
    expect(secondReceipt).toMatchObject({
      commandId: 'http-local-2',
      accepted: false,
      error: { code: 'disposed', retryable: false },
    })
    expect(thirdReceipt).toMatchObject({
      commandId: 'http-local-3',
      accepted: false,
      error: { code: 'disposed', retryable: false },
    })
    expect(commandFetchCalls()).toHaveLength(1)
  })

  it('times out a stalled POST and releases the queued command', async () => {
    const stalled = deferred<Response>()
    let posts = 0
    fetchMock.mockImplementation((input, init: RequestInit) => {
      if (input !== COMMAND_ENDPOINT) return Promise.resolve(mockResponse(VALID_WIRE))
      if (++posts > 1) return Promise.resolve(mockCommandResponse())
      init.signal?.addEventListener('abort', () => stalled.reject(init.signal?.reason), {
        once: true,
      })
      return stalled.promise
    })
    const adapter = new HttpAdapter()
    await flushPoll()
    const first = adapter.dispatch({ type: 'set-requested-tps', value: 25 })
    const queued = adapter.dispatch({ type: 'set-sender-workers', value: 7 })
    await flushPoll()
    const init = commandFetchCalls()[0]?.[1] as RequestInit

    await vi.advanceTimersByTimeAsync(4_999)
    expect(init.signal?.aborted).not.toBe(true)
    expect(commandFetchCalls()).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(init.signal?.aborted).toBe(true)
    await expect(first).resolves.toMatchObject({
      accepted: false,
      error: { code: 'unavailable', message: 'command request timed out', retryable: true },
    })
    await expect(queued).resolves.toMatchObject({ accepted: true })
    expect(commandFetchCalls()).toHaveLength(2)
    expect(adapter.getSnapshot().connectionState).toBe('connected')
    adapter.dispose()
  })

  it('serializes live commands in FIFO order and continues after failure', async () => {
    const responses = [deferred<Response>(), deferred<Response>(), deferred<Response>()]
    let responseIndex = 0
    fetchMock.mockImplementation((input) =>
      input === COMMAND_ENDPOINT
        ? responses[responseIndex++]!.promise
        : Promise.resolve(mockResponse(VALID_WIRE)),
    )
    const adapter = new HttpAdapter()

    await flushPoll()
    const receipts = [
      adapter.dispatch({ type: 'set-requested-tps', value: 25 }),
      adapter.dispatch({ type: 'set-sender-workers', value: 7 }),
      adapter.dispatch({ type: 'set-throttler-installed', value: false }),
    ]
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()[0]?.[1]).toMatchObject({
      body: '{"action":"set-requested-tps","value":25}',
    })

    responses[0].resolve(mockCommandResponse(204))
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(2)
    expect(commandFetchCalls()[1]?.[1]).toMatchObject({
      body: '{"action":"set-sender-workers","value":7}',
    })

    responses[1].resolve(mockCommandResponse(503))
    await flushPoll()
    expect(commandFetchCalls()).toHaveLength(3)
    expect(commandFetchCalls()[2]?.[1]).toMatchObject({
      body: '{"action":"set-throttler-installed","value":false}',
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

  it('keeps polling beside POST and refuses queued changes once stopping', async () => {
    const nextPoll = deferred<Response>()
    const command = deferred<Response>()
    let pollCount = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return command.promise
      return ++pollCount === 1 ? Promise.resolve(mockResponse(VALID_WIRE)) : nextPoll.promise
    })
    const adapter = new HttpAdapter()
    await flushPoll()
    const first = adapter.dispatch({ type: 'set-requested-tps', value: 25 })
    const queued = adapter.dispatch({ type: 'set-sender-workers', value: 7 })
    await flushPoll()

    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(snapshotFetchCalls()).toHaveLength(2)
    nextPoll.resolve(mockResponse(withRunState('stopping')))
    await flushPoll()
    const stopping = adapter.getSnapshot()
    expect(stopping).toMatchObject({ revision: 2, runState: 'stopping' })

    command.resolve(mockCommandResponse(204))
    await expect(first).resolves.toMatchObject({ accepted: true, snapshotRevision: 2 })
    await expect(queued).resolves.toMatchObject({ accepted: false, snapshotRevision: 2 })
    expect(adapter.getSnapshot()).toBe(stopping)
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it('assigns monotonic invocation IDs across local rejection and HTTP dispatch', async () => {
    fetchMock.mockImplementation((input) =>
      Promise.resolve(
        input === COMMAND_ENDPOINT ? mockCommandResponse(204) : mockResponse(VALID_WIRE),
      ),
    )
    const adapter = new HttpAdapter()
    const unavailable = await adapter.dispatch({ type: 'set-requested-tps', value: 25 })
    await flushPoll()
    const first = adapter.dispatch({ type: 'set-reader-workers', value: 2 })
    const second = adapter.dispatch({ type: 'set-sender-workers', value: 7 })
    const receipts = [unavailable, ...(await Promise.all([first, second]))]
    expect(receipts.map(({ commandId }) => commandId)).toEqual([
      'http-local-1',
      'http-local-2',
      'http-local-3',
    ])
    expect(receipts.map(({ accepted }) => accepted)).toEqual([false, true, true])
    receipts.forEach((receipt) => expectDeepFrozen(receipt))
    adapter.dispose()
  })
})
