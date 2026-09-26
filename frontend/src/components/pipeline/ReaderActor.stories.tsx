import type { Meta, StoryObj } from '@storybook/react-vite'
import type { LiveControls } from '../../hooks/useDesiredControl'
import type {
  LoadgenSnapshot,
  NumericControlSnapshot,
} from '../../model/loadgen'
import { PipelineSvg } from './PipelineSvg'
import './PipelineSvg.css'

const noop = () => {}
const accept = async () => true

function numericControl(applied: number, unit: string): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 0,
    max: 250_000,
    step: 1,
    unit,
    applyMode: 'immediate',
  }
}

function desiredControl<T>(applied: T) {
  return {
    applied,
    desired: applied,
    phase: 'idle' as const,
    error: null,
    available: true,
    preview: noop,
    commit: accept,
    cancel: noop,
  }
}

function liveControls(snapshot: LoadgenSnapshot): LiveControls {
  return {
    readerWorkers: desiredControl(snapshot.reader.workers.applied ?? 0),
    readBatchSize: desiredControl(snapshot.reader.readBatchSize.applied ?? 0),
    requestedTps: desiredControl(snapshot.throttler.requestedTps.applied ?? 0),
    installationMode: desiredControl(snapshot.throttler.installationMode.applied ?? 'installed'),
    senderWorkers: desiredControl(snapshot.sender.workers.applied ?? 0),
    timeoutMs: desiredControl(snapshot.sender.timeoutMs.applied ?? 0),
  }
}

type ReaderState = 'idle' | 'reading' | 'backpressured' | 'draining' | 'error'

function snapshotFor(state: ReaderState): LoadgenSnapshot {
  const active = state === 'reading' || state === 'backpressured'
  const draining = state === 'draining'
  const connectionError = state === 'error'
  const runState = active ? 'running' : draining ? 'paused' : 'idle'
  const readerWorkers = active ? 3 : 2
  const readerChannel = {
    id: 'reader-to-throttler' as const,
    from: 'reader' as const,
    to: 'throttler' as const,
    capacity: numericControl(state === 'backpressured' ? 8 : 32, 'batches'),
    sentBatchesTotal: active ? 120 : 0,
    sentTransactionsTotal: active ? 12_000 : 0,
    receivedBatchesTotal: active && state === 'backpressured' ? 72 : active ? 118 : 0,
    receivedTransactionsTotal: active && state === 'backpressured' ? 7_200 : active ? 11_800 : 0,
    depthBatches: active && state === 'backpressured' ? 8 : active ? 2 : 0,
    bufferedTransactions: active && state === 'backpressured' ? 800 : active ? 200 : 0,
    handoffBatches: active && state === 'backpressured' ? 4 : 0,
    handoffBatchesTotal: active && state === 'backpressured' ? 18 : 0,
    blockedSenders: active && state === 'backpressured' ? 2 : 0,
    oldestBlockedSenderMs: active && state === 'backpressured' ? 1_250 : 0,
    inputBatchesPerSecond: active ? 12 : 0,
    outputBatchesPerSecond: active && state === 'backpressured' ? 7 : active ? 12 : 0,
    inputTransactionsPerSecond: active ? 1_200 : 0,
    outputTransactionsPerSecond: active && state === 'backpressured' ? 700 : active ? 1_200 : 0,
    inputTps: active ? 1_200 : 0,
    outputTps: active && state === 'backpressured' ? 700 : active ? 1_200 : 0,
    throughputTps: active && state === 'backpressured' ? 700 : active ? 1_200 : 0,
    blockedMs: active && state === 'backpressured' ? 1_250 : null,
    trend: state === 'backpressured' ? 'rising' as const : active ? 'steady' as const : 'unknown' as const,
    displayedPressure: state === 'backpressured' ? 1 : active ? 0.2 : 0,
    flowState: connectionError ? 'connection-error' as const : state === 'backpressured' ? 'backpressure' as const : active ? 'normal' as const : 'stopped' as const,
  }
  const workerControl = numericControl(readerWorkers, 'workers')
  const batchControl = numericControl(100, 'rows')
  const tpsControl = numericControl(active ? 120_000 : 0, 'tx/s')
  const modeControl = {
    applied: 'installed' as const,
    pending: null,
    applyMode: 'immediate' as const,
    writable: true,
    unavailableReason: null,
  }

  return {
    revision: 1,
    adapterKind: 'simulation',
    connectionState: connectionError ? 'error' : 'connected',
    runState,
    elapsedMs: active ? 12_000 : 0,
    totalTransactions: active ? 12_000 : 0,
    policy: null,
    reader: {
      id: 'reader',
      workers: workerControl,
      liveWorkers: active ? 3 : draining ? 2 : readerWorkers,
      drainingWorkers: draining ? 2 : 0,
      idleWorkers: state === 'idle' || state === 'error' ? 2 : draining ? 1 : 0,
      readingWorkers: state === 'reading' ? 3 : draining ? 1 : 0,
      blockedWorkers: state === 'backpressured' ? 3 : 0,
      drainingIdleWorkers: draining ? 1 : 0,
      drainingReadingWorkers: draining ? 1 : 0,
      drainingBlockedWorkers: 0,
      readBatchSize: batchControl,
      readTps: active ? 1_200 : null,
      configuredCapacityTps: connectionError ? null : 1_500,
      limitationReason: state === 'backpressured' ? 'downstream-backpressure' : null,
      rowsRead: active ? 12_000 : null,
      sourceDirectory: connectionError ? undefined : 'fixture source',
      sourceError: null,
      state: runState,
    },
    throttler: {
      id: 'throttler', requestedTps: tpsControl, installationMode: modeControl,
      admittedTps: active ? (state === 'backpressured' ? 700 : 1_200) : null,
      limitedMs: state === 'backpressured' ? 1_250 : null, state: runState,
    },
    readerChannel,
    senderChannel: { ...readerChannel, id: 'throttler-to-sender', from: 'throttler', to: 'sender' },
    sender: {
      id: 'sender', workers: workerControl, liveWorkers: active ? 3 : 2, drainingWorkers: 0,
      idleWorkers: active ? 0 : 2, inFlightWorkers: active ? 3 : 0, backoffWorkers: 0,
      drainingIdleWorkers: 0, drainingInFlightWorkers: 0, drainingBackoffWorkers: 0,
      timeoutMs: numericControl(5_000, 'ms'), retryPolicy: null,
      attemptedTps: active ? 1_200 : null, retryAttemptedTps: 0, terminalFailedTps: 0,
      inFlightRequests: active ? 3 : 0, attemptsStartedTotal: active ? 12_000 : 0,
      retryAttemptsStartedTotal: 0, successfulResponses: active ? 11_900 : 0, failedResponses: 0,
      retries: 0, timeoutsTotal: 0, terminalFailedBatchesTotal: 0, terminalFailedTransactionsTotal: 0,
      ambiguousTimeoutTransactionsTotal: 0, duplicateRiskTransactionsTotal: 0,
      ambiguousTerminalTransactionsTotal: 0, state: runState,
    },
    http: {
      id: 'http', connectionState: connectionError ? 'error' : 'connected', statusCode: connectionError ? null : 200,
      lastOutcome: connectionError ? 'network-error' : 'http-response', throughputTps: active ? 1_200 : null,
      inFlightRequests: active ? 3 : 0, requestsStartedTotal: active ? 12_000 : 0,
      requestsCompletedTotal: active ? 11_900 : 0, requestsSucceededTotal: active ? 11_900 : 0,
      requestsFailedTotal: 0, requestsTimedOutTotal: 0, networkErrorsTotal: connectionError ? 1 : 0, latencyP95Ms: active ? 42 : null,
    },
    target: {
      id: 'target', endpoint: connectionError ? null : 'fixture://target', acceptedTps: active ? 1_200 : null,
      rejectedTps: 0, latencyP95Ms: active ? 42 : null, http200Responses: active ? 11_900 : null,
      http503Responses: 0, connectionState: connectionError ? 'error' : 'connected',
    },
  }
}

function ReaderStory({ state }: { state: ReaderState }) {
  const snapshot = snapshotFor(state)
  return (
    <PipelineSvg
      snapshot={snapshot}
      selectedId="reader"
      onSelect={noop}
      onChannelCapacityChange={noop}
      liveControls={liveControls(snapshot)}
    />
  )
}

const meta = {
  title: 'Pipeline/Reader',
  component: ReaderStory,
} satisfies Meta<typeof ReaderStory>

export default meta
type Story = StoryObj<typeof meta>

export const Idle: Story = { args: { state: 'idle' } }
export const Reading: Story = { args: { state: 'reading' } }
export const Backpressured: Story = { args: { state: 'backpressured' } }
export const Draining: Story = { args: { state: 'draining' } }
export const Error: Story = { args: { state: 'error' } }
