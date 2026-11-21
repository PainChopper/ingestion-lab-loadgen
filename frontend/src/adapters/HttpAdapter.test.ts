import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChannelFlowStateDeriver } from '../model/channelFlowState'
import { readWireFixture } from '../test/wireFixture'
import type { LoadgenCommand, LoadgenTelemetrySnapshot, RunState } from '../model/loadgen'
import { HttpAdapter } from './HttpAdapter'
import { getInspectorViewModel } from '../components/inspectorViewModel'

interface MockResponseOptions {
  readonly status?: number
  readonly contentType?: string | null
  readonly jsonError?: Error
}

const FIXTURE = readWireFixture()
// Small maximum keeps boundary/FIFO tests readable; the contract test uses the shared fixture.
const WIRE = {
  ...FIXTURE,
  run: { ...FIXTURE.run, state: 'running' },
  config: { ...FIXTURE.config, requestedTpsMax: 400 },
}

function withRunState(state: RunState) {
  return { ...WIRE, run: { ...WIRE.run, state } }
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

function expectDeepFrozen(value: unknown, seen = new Set<object>()): void {
  if (value === null || typeof value !== 'object' || seen.has(value)) return
  seen.add(value)
  expect(Object.isFrozen(value)).toBe(true)
  Object.values(value).forEach((child) => expectDeepFrozen(child, seen))
}

const malformedCases = [
  ...[
    ['reader', 'readTps'],
    ['throttler', 'admittedTps'],
    ['readerChannel', 'inputTransactionsPerSecond'],
    ['readerChannel', 'outputTransactionsPerSecond'],
    ['senderChannel', 'inputTransactionsPerSecond'],
    ['senderChannel', 'outputTransactionsPerSecond'],
  ].map(([side, field]) => ({
    name: `removed rate ${side}.${field}`,
    result: async () => mockResponse({ ...WIRE, [side]: { ...WIRE[side], [field]: 0 } }),
  })),
  ...[
    ['capturedAtMs'],
    ['readerChannel', 'inputTransactionsTotal'],
    ['readerChannel', 'outputTransactionsTotal'],
    ['senderChannel', 'inputTransactionsTotal'],
    ['senderChannel', 'outputTransactionsTotal'],
  ].map(([side, field]) => ({
    name: `required monotonic field ${side}.${field ?? ''}`,
    result: async () => {
      const body = structuredClone(WIRE)
      if (field === undefined) delete body[side]
      else delete body[side][field]
      return mockResponse(body)
    },
  })),
  ...(['reader', 'sender'] as const).flatMap((pool) =>
    [
      { activity: pool === 'reader' ? 'inFlight' : 'reading', draining: false },
      { activity: 'unknown', draining: false },
      { draining: false },
      { activity: null, draining: false },
      { activity: 1, draining: false },
      { activity: 'idle' },
      { activity: 'idle', draining: null },
      { activity: 'idle', draining: 'false' },
      { activity: 'idle', draining: false, id: 1 },
      null,
    ].map((item, index) => ({
      name: `${pool} malformed worker item ${index}`,
      result: async () =>
        mockResponse({ ...WIRE, [pool]: { ...WIRE[pool], workerStates: [item] } }),
    })),
  ),
  ...(['reader', 'sender'] as const).flatMap((pool) =>
    [null, undefined, {}].map((workerStates) => ({
      name: `${pool} malformed worker list ${String(workerStates)}`,
      result: async () => mockResponse({ ...WIRE, [pool]: { ...WIRE[pool], workerStates } }),
    })),
  ),
  {
    name: 'required field',
    result: async () => {
      const body = structuredClone(WIRE)
      delete body.reader.rowsRead
      return mockResponse(body)
    },
  },
  {
    name: 'primitive type',
    result: async () =>
      mockResponse({ ...WIRE, throttler: { ...WIRE.throttler, installed: 'true' } }),
  },
  {
    name: 'nonnegative number',
    result: async () => mockResponse({ ...WIRE, reader: { ...WIRE.reader, rowsRead: -1 } }),
  },
  {
    name: 'integer',
    result: async () => mockResponse({ ...WIRE, run: { ...WIRE.run, elapsedMs: 0.5 } }),
  },
  {
    name: 'safe integer',
    result: async () =>
      mockResponse({ ...WIRE, run: { ...WIRE.run, elapsedMs: Number.MAX_SAFE_INTEGER + 1 } }),
  },
  {
    name: 'finite number',
    result: async () => mockResponse({ ...WIRE, reader: { ...WIRE.reader, rowsRead: Infinity } }),
  },
  {
    name: 'run enum',
    result: async () => mockResponse({ ...WIRE, run: { ...WIRE.run, state: 'unknown' } }),
  },
  {
    name: 'positive batch size',
    result: async () => mockResponse({ ...WIRE, config: { ...WIRE.config, readBatchSize: 0 } }),
  },
  ...(['readerWorkersMax', 'senderWorkersMax', 'requestedTpsMax'] as const).map((field) => ({
    name: `config positive ${field}`,
    result: async () => mockResponse({ ...WIRE, config: { ...WIRE.config, [field]: 0 } }),
  })),
  {
    name: 'strict config shape',
    result: async () => mockResponse({ ...WIRE, config: { ...WIRE.config, extra: true } }),
  },
  ...[
    ['config', 'readerWorkers'],
    ['config', 'senderWorkers'],
    ['config', 'throttlerRequestedTps'],
    ['reader', 'readBatchSize'],
    ['reader', 'sourceDirectory'],
    ['readerChannel', 'capacity'],
    ['senderChannel', 'capacity'],
    ['readerChannel', 'bufferedTransactions'],
    ['senderChannel', 'bufferedTransactions'],
  ].map(([side, field]) => ({
    name: `removed field ${side}.${field}`,
    result: async () => mockResponse({ ...WIRE, [side]: { ...WIRE[side], [field]: 1 } }),
  })),
  ...['category', 'relativePath'].map((field) => ({
    name: `removed source error ${field}`,
    result: async () =>
      mockResponse({
        ...WIRE,
        reader: { ...WIRE.reader, sourceError: { ...WIRE.reader.sourceError, [field]: '' } },
      }),
  })),
  {
    name: 'required source error string',
    result: async () => {
      const body = structuredClone(WIRE)
      delete body.reader.sourceError.operation
      return mockResponse(body)
    },
  },
  {
    name: 'source error string type',
    result: async () =>
      mockResponse({
        ...WIRE,
        reader: { ...WIRE.reader, sourceError: { ...WIRE.reader.sourceError, message: null } },
      }),
  },
  { name: 'body object', result: async () => mockResponse(null) },
  {
    name: 'JSON syntax',
    result: async () => mockResponse(null, { jsonError: new SyntaxError('invalid JSON') }),
  },
  {
    name: 'JSON Content-Type',
    result: async () => mockResponse(WIRE, { contentType: 'text/plain' }),
  },
  { name: 'required Content-Type', result: async () => mockResponse(WIRE, { contentType: null }) },
  {
    name: 'network',
    result: async () => {
      throw new TypeError('offline')
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

  it('keeps previous worker lists unchanged when a later response has empty pools', async () => {
    const body = structuredClone(WIRE)
    const previousBody = JSON.stringify(body)
    fetchMock.mockResolvedValueOnce(mockResponse(body))
    const adapter = new HttpAdapter()
    await flushPoll()
    const previous = adapter.getSnapshot()
    const previousStates = JSON.stringify([
      previous.reader.workerStates,
      previous.sender.workerStates,
    ])
    fetchMock.mockResolvedValue(
      mockResponse({
        ...WIRE,
        reader: { ...WIRE.reader, workerStates: [] },
        sender: { ...WIRE.sender, workerStates: [] },
      }),
    )
    await vi.advanceTimersByTimeAsync(1_000)
    const empty = adapter.getSnapshot()
    expect(empty.connectionState).toBe('connected')
    expect(empty.reader.workerStates).toEqual([])
    expect(empty.sender.workerStates).toEqual([])
    const displayed = new ChannelFlowStateDeriver().derive(empty, NOW_MS)
    expect(getInspectorViewModel(displayed, 'reader')?.rows[0].value).toBe('11 / 0 / 0')
    expect(getInspectorViewModel(displayed, 'sender')?.rows[0].value).toBe('41 / 0 / 0')
    expect(JSON.stringify([previous.reader.workerStates, previous.sender.workerStates])).toBe(
      previousStates,
    )
    expect(JSON.stringify(body)).toBe(previousBody)
    expect(previous.reader.workerStates).not.toBe(body.reader.workerStates)
    expect(previous.reader.workerStates[0]).not.toBe(body.reader.workerStates[0])
    expectDeepFrozen(empty)
    adapter.dispose()
  })

  it.each(['starting', 'running', 'stopping', 'faulted'] as const)(
    'maps the complete shared Go fixture with independent measurements in %s',
    async (state) => {
      const body = structuredClone(FIXTURE)
      body.run.state = state
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
      fetchMock.mockResolvedValueOnce(mockResponse(body))
      const adapter = new HttpAdapter()
      await flushPoll()
      const snapshot = adapter.getSnapshot()

      expect(snapshot).toMatchObject({
        revision: 1,
        adapterKind: 'http',
        connectionState: 'connected',
        runState: state,
        elapsedMs: 102,
        totalTransactions: 101,
      })
      expect(snapshot.reader).toMatchObject({
        readBatchSize: 20,
        readTps: null,
        rowsRead: 22,
        sourcePath: 'reader-source',
      })
      expect(snapshot.reader.sourceError).toEqual(body.reader.sourceError)
      for (const pool of ['reader', 'sender'] as const) {
        const counts = new Map<string, number>()
        for (const item of snapshot[pool].workerStates) {
          const pair = JSON.stringify(item)
          counts.set(pair, (counts.get(pair) ?? 0) + 1)
        }
        expect(Object.fromEntries(counts)).toEqual(
          pool === 'reader'
            ? {
                '{"activity":"reading","draining":false}': 2,
                '{"activity":"blocked","draining":true}': 1,
                '{"activity":"idle","draining":false}': 1,
              }
            : {
                '{"activity":"inFlight","draining":true}': 1,
                '{"activity":"backoff","draining":false}': 3,
                '{"activity":"idle","draining":true}': 1,
              },
        )
      }
      expect(snapshot.reader.workers).toMatchObject({ applied: 11, min: 1, max: 7, step: 1 })
      expect(snapshot.sender.workers).toMatchObject({ applied: 41, min: 1, max: 32, step: 1 })
      expect(snapshot.throttler).toMatchObject({
        requestedTps: { applied: 31, min: 0, max: 4_000_000, step: 1 },
        admittedTps: null,
        installed: { applied: false },
      })
      expect(snapshot.readerChannel).toMatchObject({
        id: 'reader-to-throttler',
        from: 'reader',
        to: 'throttler',
        capacity: 51,
        depthBatches: 52,
        blockedSenders: 54,
        oldestBlockedSenderMs: 55,
        inputTransactionsPerSecond: null,
        outputTransactionsPerSecond: null,
      })
      expect(snapshot.senderChannel).toMatchObject({
        id: 'throttler-to-sender',
        from: 'throttler',
        to: 'sender',
        capacity: 71,
        depthBatches: 72,
        blockedSenders: 74,
        oldestBlockedSenderMs: 75,
        inputTransactionsPerSecond: null,
        outputTransactionsPerSecond: null,
      })
      expectDeepFrozen(snapshot)
      const displayed = new ChannelFlowStateDeriver().derive(snapshot, NOW_MS)
      expect(
        getInspectorViewModel(displayed, 'reader')
          ?.rows.slice(0, 2)
          .map(({ value }) => value),
      ).toEqual(['11 / 4 / 1', '1 idle · 2 reading · 1 blocked · 1 error'])
      expect(getInspectorViewModel(displayed, 'sender')?.rows.map(({ value }) => value)).toEqual([
        '41 / 5 / 2',
        '1 idle · 1 in-flight · 3 backoff',
      ])
      expect(warn).not.toHaveBeenCalled()
      fetchMock.mockResolvedValue(mockCommandResponse())
      for (const command of [
        { type: 'set-reader-workers', value: 11 },
        { type: 'set-sender-workers', value: 41 },
        { type: 'set-requested-tps', value: 4_000_001 },
      ] as const) {
        expect((await adapter.dispatch(command)).accepted).toBe(false)
      }
      expect(commandFetchCalls()).toHaveLength(0)
      if (state === 'running') {
        expect((await adapter.dispatch({ type: 'set-reader-workers', value: 7 })).accepted).toBe(
          true,
        )
      }
      adapter.dispose()
    },
  )

  it('sends exact integer TPS across the range and rejects invalid values locally', async () => {
    const wire = { ...FIXTURE, run: { ...FIXTURE.run, state: 'running' } }
    fetchMock.mockImplementation((input) =>
      Promise.resolve(input === COMMAND_ENDPOINT ? mockCommandResponse() : mockResponse(wire)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    for (const value of [0, 2_000_001, 4_000_000]) {
      expect((await adapter.dispatch({ type: 'set-requested-tps', value })).accepted).toBe(true)
    }
    for (const value of [-1, 4_000_001, 1.5, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
      expect((await adapter.dispatch({ type: 'set-requested-tps', value })).accepted).toBe(false)
    }
    expect(
      commandFetchCalls().map(([, init]) => JSON.parse((init as RequestInit).body as string).value),
    ).toEqual([0, 2_000_001, 4_000_000])
    adapter.dispose()
  })

  it('accepts zero measurements and a nullable source error', async () => {
    fetchMock.mockResolvedValueOnce(
      mockResponse({
        ...WIRE,
        reader: { ...WIRE.reader, rowsRead: 0, sourceError: null },
      }),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    expect(adapter.getSnapshot().reader).toMatchObject({
      readTps: null,
      rowsRead: 0,
      sourceError: null,
    })
    adapter.dispose()
  })

  it('computes rates only for new polls using server intervals and preserves real zero', async () => {
    const first = {
      ...WIRE,
      capturedAtMs: 100,
      reader: { ...WIRE.reader, rowsRead: 0 },
    }
    const second = {
      ...first,
      capturedAtMs: 2_600,
      reader: { ...first.reader, rowsRead: 250 },
      senderChannel: {
        ...first.senderChannel,
        inputTransactionsTotal: first.senderChannel.inputTransactionsTotal + 50,
      },
    }
    fetchMock
      .mockResolvedValueOnce(mockResponse(first))
      .mockResolvedValueOnce(mockResponse(second))
      .mockResolvedValueOnce(mockResponse({ ...second, capturedAtMs: 3_100 }))
    const adapter = new HttpAdapter()
    await flushPoll()
    expect(adapter.getSnapshot().reader.readTps).toBeNull()
    await vi.advanceTimersByTimeAsync(1_000)
    const snapshot = adapter.getSnapshot()
    expect(snapshot.reader.readTps).toBe(100)
    expect(snapshot.throttler.admittedTps).toBe(20)
    expect(adapter.getSnapshot()).toBe(snapshot)
    adapter.subscribe(() => adapter.getSnapshot())()
    expect(adapter.getSnapshot()).toBe(snapshot)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(adapter.getSnapshot().reader.readTps).toBe(0)
    expect(adapter.getSnapshot().throttler.admittedTps).toBe(0)
    adapter.dispose()
  })

  it('accepts source error strings without imposing operation or path conventions', async () => {
    const sourceError = {
      operation: 'future-operation',
      path: '',
      message: '',
    }
    fetchMock.mockResolvedValueOnce(
      mockResponse({
        ...WIRE,
        config: { ...WIRE.config, sourcePath: '' },
        reader: { ...WIRE.reader, sourceError },
      }),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    expect(adapter.getSnapshot()).toMatchObject({
      connectionState: 'connected',
      reader: { sourcePath: '', sourceError },
    })
    adapter.dispose()
  })

  it('uses the Sender worker range from the snapshot', async () => {
    const senderWorkersMax = 35
    fetchMock.mockResolvedValueOnce(
      mockResponse({
        ...WIRE,
        config: { ...WIRE.config, senderWorkersMax },
        sender: { ...WIRE.sender, workers: 35 },
      }),
    )
    const adapter = new HttpAdapter()
    await flushPoll()

    expect(adapter.getSnapshot()).toMatchObject({
      connectionState: 'connected',
      sender: { workers: { applied: 35, min: 1, max: 35, step: 1, applyMode: 'immediate' } },
    })
    adapter.dispose()
  })

  it('starts one GET with an unknown frozen run and unavailable controls', () => {
    const adapter = new HttpAdapter()
    const snapshot = adapter.getSnapshot()

    expect(adapter.kind).toBe('http')
    expect(snapshot).toMatchObject({
      revision: 0,
      connectionState: 'connecting',
      runState: null,
      reader: { workers: { applied: null, applyMode: 'unavailable' } },
      senderChannel: { outputTransactionsPerSecond: null },
    })
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

  it.each([0, 8_192])(
    'maps readerChannel capacity %i to read-only telemetry',
    async (readerChannelCapacity) => {
      const wire = {
        ...WIRE,
        config: { ...WIRE.config, readerChannelCapacity },
      }
      fetchMock.mockResolvedValueOnce(mockResponse(wire))
      const adapter = new HttpAdapter()

      await flushPoll()

      expect(adapter.getSnapshot().readerChannel.capacity).toEqual(readerChannelCapacity)
      adapter.dispose()
    },
  )

  it.each(malformedCases)('publishes one neutral error snapshot for $name', async ({ result }) => {
    fetchMock.mockImplementationOnce(result)
    const adapter = new HttpAdapter()
    const initial = adapter.getSnapshot()

    await flushPoll()
    const snapshot = adapter.getSnapshot()

    expect(snapshot).not.toBe(initial)
    expect(snapshot).toMatchObject({
      revision: 1,
      connectionState: 'error',
      runState: null,
      reader: { readTps: null },
    })
    expectDeepFrozen(snapshot)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    adapter.dispose()
  })

  it.each([
    { path: 'body', wire: { ...WIRE, extra: true } },
    { path: 'run', wire: { ...WIRE, run: { ...WIRE.run, extra: true } } },
    { path: 'sender', wire: { ...WIRE, sender: { ...WIRE.sender, extra: true } } },
    {
      path: 'reader.sourceError',
      wire: {
        ...WIRE,
        reader: {
          ...WIRE.reader,
          sourceError: {
            operation: 'read',
            path: 'broken.parquet',
            message: 'Could not read file',
            extra: true,
          },
        },
      },
    },
  ])('rejects an unknown field in $path as a schema error and recovers', async ({ path, wire }) => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    fetchMock.mockResolvedValueOnce(mockResponse(wire)).mockResolvedValueOnce(mockResponse(WIRE))
    const adapter = new HttpAdapter()

    await flushPoll()
    expect(adapter.getSnapshot()).toMatchObject({
      revision: 1,
      connectionState: 'error',
      runState: null,
      reader: { readTps: null },
    })
    expect(warn).toHaveBeenCalledExactlyOnceWith(
      expect.stringContaining(`LoadGen snapshot decoding failed: snapshot ${path}:`),
    )

    await vi.advanceTimersByTimeAsync(1_000)
    expect(adapter.getSnapshot()).toMatchObject({
      revision: 2,
      connectionState: 'connected',
      runState: WIRE.run.state,
      reader: { rowsRead: WIRE.reader.rowsRead },
    })
    expect(warn).toHaveBeenCalledTimes(1)
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
    expect(adapter.getSnapshot()).toMatchObject({ revision: 1, connectionState: 'error' })
    expect(response.json).not.toHaveBeenCalled()
    expect(init.signal?.aborted).toBe(true)
    adapter.dispose()
  })

  it('retains decoded telemetry on failure, disables controls and recovers on success', async () => {
    const recoveredWire = {
      ...WIRE,
      capturedAtMs: WIRE.capturedAtMs + 2_000,
      run: { ...WIRE.run, elapsedMs: 67_890, totalTransactions: 84_000 },
      reader: { ...WIRE.reader, rowsRead: WIRE.reader.rowsRead + 4_000 },
    }
    fetchMock
      .mockResolvedValueOnce(mockResponse(WIRE))
      .mockRejectedValueOnce(new TypeError('network unavailable'))
      .mockResolvedValueOnce(mockResponse(recoveredWire))
    const adapter = new HttpAdapter()

    await flushPoll()
    const first = adapter.getSnapshot()

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    const stale = adapter.getSnapshot()
    expect(stale).toMatchObject({
      revision: 2,
      connectionState: 'error',
      runState: first.runState,
      elapsedMs: first.elapsedMs,
      totalTransactions: first.totalTransactions,
      reader: {
        readTps: first.reader.readTps,
        workers: { applied: first.reader.workers.applied, applyMode: 'unavailable' },
      },
      senderChannel: {
        outputTransactionsPerSecond: first.senderChannel.outputTransactionsPerSecond,
      },
    })
    expectDeepFrozen(stale)
    const derived = new ChannelFlowStateDeriver().derive(stale, Date.now())
    expect(derived.readerChannel.flowState).toBe('connection-error')
    expect(derived.senderChannel.flowState).toBe('connection-error')
    await expect(adapter.dispatch({ type: 'set-requested-tps', value: 25 })).resolves.toMatchObject(
      { accepted: false, error: { code: 'unavailable' } },
    )
    expect(commandFetchCalls()).toHaveLength(0)

    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot()).toMatchObject({
      revision: 3,
      connectionState: 'connected',
      elapsedMs: 67_890,
      totalTransactions: 84_000,
      reader: { readTps: 2_000, workers: { applyMode: 'immediate' } },
    })
    adapter.dispose()
  })

  it('warns on changed decoding messages and clears the deduplication after recovery', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const invalidRun = mockResponse({ ...WIRE, run: { ...WIRE.run, state: 'unknown' } })
    const invalidJson = mockResponse(null, { jsonError: new SyntaxError('bad snapshot JSON') })
    fetchMock
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(mockResponse(null, { status: 503 }))
      .mockResolvedValueOnce(invalidJson)
      .mockResolvedValueOnce(invalidJson)
      .mockResolvedValueOnce(invalidRun)
      .mockResolvedValueOnce(mockResponse(WIRE))
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
      expect(snapshot).toMatchObject({
        revision: 1,
        connectionState: 'connected',
        runState: state,
        reader: { workers: { applied: 11 }, readTps: null },
        sender: { workers: { applied: 41 } },
        throttler: { requestedTps: { applied: 31 } },
      })
      expect(snapshot.reader.workers.applyMode).toBe('unavailable')
      expect(snapshot.sender.workers.applyMode).toBe('unavailable')
      expect(snapshot.throttler.requestedTps.applyMode).toBe('unavailable')
      expect(snapshot.throttler.installed.applyMode).toBe('unavailable')
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

    first.resolve(mockResponse(WIRE))
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
    expect(adapter.getSnapshot()).toMatchObject({ revision: 1, connectionState: 'error' })
    expect(warn).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1_000)
    expect(snapshotFetchCalls()).toHaveLength(2)
    recovery.resolve(mockResponse(WIRE))
    await flushPoll()
    expect(adapter.getSnapshot()).toMatchObject({ revision: 2, connectionState: 'connected' })
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
    first.resolve(mockResponse(WIRE))
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
        request.resolve(mockResponse(WIRE))
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

  it('sends an exact immediate Sender command while running and reads the next snapshot', async () => {
    const firstWire = withRunState('running')
    const appliedWire = {
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

    await expect(adapter.dispatch({ type: 'set-sender-workers', value: 7 })).resolves.toMatchObject(
      {
        accepted: true,
      },
    )
    expect(commandFetchCalls().map(([, init]) => (init as RequestInit).body)).toEqual([
      '{"action":"set-sender-workers","value":7}',
    ])
    await vi.advanceTimersByTimeAsync(1_000)
    await flushPoll()
    expect(adapter.getSnapshot().sender.workers.applied).toBe(7)
    adapter.dispose()
  })

  it('sends exact Reader worker commands while running and reads each applied value from the next snapshot', async () => {
    const firstWire = withRunState('running')
    const oneReaderWire = {
      ...firstWire,
      reader: { ...firstWire.reader, workers: 1 },
    }
    const sevenReaderWire = {
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
  })

  it.each([true, false])(
    'sends immediate throttler installed=%s and confirms only by poll',
    async (installed) => {
      let wire = {
        ...WIRE,
        throttler: { ...WIRE.throttler, requestedTps: 0, installed: !installed },
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
        step: 1,
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
            ? { ...WIRE, run: { ...WIRE.run, state: 'running' } }
            : { ...WIRE, throttler: { ...WIRE.throttler, requestedTps: '25' } },
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
      error: { code: 'unavailable' },
    })
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })

  it('accepts 204 without a response body and leaves snapshot authority intact', async () => {
    fetchMock.mockImplementation((input) =>
      input === COMMAND_ENDPOINT
        ? Promise.resolve(mockCommandResponse(204))
        : Promise.resolve(mockResponse(WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'set-sender-workers', value: 7 })

    expect(receipt).toEqual({
      accepted: true,
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
      input === COMMAND_ENDPOINT ? Promise.resolve(response) : Promise.resolve(mockResponse(WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()

    const receipt = await adapter.dispatch({ type: 'set-sender-workers', value: 7 })

    expect(receipt.accepted).toBe(true)
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
      accepted: false,
      error: {
        code: 'unavailable',
        message: 'command is not available in the HTTP adapter',
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

  it.each([{ status: 409 }, { status: 503 }])(
    'maps HTTP $status to an unavailable receipt',
    async ({ status }) => {
      const { response, accessors } = responseWithThrowingRepresentation(status)
      fetchMock.mockImplementation((input) =>
        input === COMMAND_ENDPOINT
          ? Promise.resolve(response)
          : Promise.resolve(mockResponse(WIRE)),
      )
      const adapter = new HttpAdapter()
      await flushPoll()
      const initial = adapter.getSnapshot()

      const receipt = await adapter.dispatch({ type: 'set-throttler-installed', value: false })

      expect(receipt).toEqual({
        accepted: false,
        error: {
          code: 'unavailable',
          message: `command request failed with HTTP status ${status}`,
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
        : Promise.resolve(mockResponse(WIRE)),
    )
    const adapter = new HttpAdapter()
    await flushPoll()
    const initial = adapter.getSnapshot()

    const receipt = await adapter.dispatch({ type: 'set-requested-tps', value: 25 })

    expect(receipt).toEqual({
      accepted: false,
      error: {
        code: 'unavailable',
        message: 'command request failed due to a network error',
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
      accepted: false,
      error: {
        code: 'disposed',
        message: 'http adapter is disposed',
      },
    })
    expectDeepFrozen(receipt)
    expect(snapshotFetchCalls()).toHaveLength(1)
    expect(commandFetchCalls()).toHaveLength(0)
  })

  it('aborts the active POST and disposes buffered commands without POST', async () => {
    const inFlight = deferred<Response>()
    fetchMock.mockImplementation((input, init: RequestInit) => {
      if (input !== COMMAND_ENDPOINT) return Promise.resolve(mockResponse(WIRE))
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
      accepted: false,
      error: { code: 'disposed' },
    })
    expect(secondReceipt).toMatchObject({
      accepted: false,
      error: { code: 'disposed' },
    })
    expect(thirdReceipt).toMatchObject({
      accepted: false,
      error: { code: 'disposed' },
    })
    expect(commandFetchCalls()).toHaveLength(1)
  })

  it('times out a stalled POST and releases the queued command', async () => {
    const stalled = deferred<Response>()
    let posts = 0
    fetchMock.mockImplementation((input, init: RequestInit) => {
      if (input !== COMMAND_ENDPOINT) return Promise.resolve(mockResponse(WIRE))
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
      error: { code: 'unavailable', message: 'command request timed out' },
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
        : Promise.resolve(mockResponse(WIRE)),
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
    expect(settled.map(({ accepted }) => accepted)).toEqual([true, false, true])
    expect(settled[1]?.error).toMatchObject({
      code: 'unavailable',
    })
    adapter.dispose()
  })

  it('keeps polling beside POST and refuses queued changes once stopping', async () => {
    const nextPoll = deferred<Response>()
    const command = deferred<Response>()
    let pollCount = 0
    fetchMock.mockImplementation((input) => {
      if (input === COMMAND_ENDPOINT) return command.promise
      return ++pollCount === 1 ? Promise.resolve(mockResponse(WIRE)) : nextPoll.promise
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
    await expect(first).resolves.toMatchObject({ accepted: true })
    await expect(queued).resolves.toMatchObject({ accepted: false })
    expect(adapter.getSnapshot()).toBe(stopping)
    expect(commandFetchCalls()).toHaveLength(1)
    adapter.dispose()
  })
})
