import type { Meta, StoryObj } from '@storybook/react-vite'
import type { LiveControls } from '../../hooks/useDesiredControl'
import type {
  LoadgenSnapshot,
  NumericControlSnapshot,
  SenderWorkerSlotSnapshot,
} from '../../model/loadgen'
import { PipelineSvg } from './PipelineSvg'
import './PipelineSvg.css'

const noop = () => {}
const accept = async () => true

function numericControl(applied: number, unit: string): NumericControlSnapshot {
  return {
    applied, preview: null, pending: null, min: 0, max: 250_000, step: 1, unit,
    applyMode: 'immediate',
  }
}

function desiredControl<T>(applied: T) {
  return {
    applied, desired: applied, phase: 'idle' as const, error: null, available: true,
    preview: noop, commit: accept, cancel: noop,
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

type SenderState = 'idle' | 'sending' | 'retry-backoff' | 'terminal-failure' | 'draining'

function senderSlots(state: SenderState): readonly SenderWorkerSlotSnapshot[] {
  if (state === 'idle') return [
    { workerId: 1, activity: 'idle', lifecycle: 'active', terminalError: false },
    { workerId: 2, activity: 'idle', lifecycle: 'active', terminalError: false },
  ]
  if (state === 'sending') return [
    { workerId: 1, activity: 'in-flight', lifecycle: 'active', terminalError: false },
    { workerId: 2, activity: 'in-flight', lifecycle: 'active', terminalError: false },
    { workerId: 3, activity: 'in-flight', lifecycle: 'active', terminalError: false },
  ]
  if (state === 'retry-backoff') return [
    { workerId: 1, activity: 'backoff', lifecycle: 'active', terminalError: false },
    { workerId: 2, activity: 'in-flight', lifecycle: 'active', terminalError: false },
    { workerId: 3, activity: 'backoff', lifecycle: 'active', terminalError: false },
  ]
  if (state === 'terminal-failure') return [
    { workerId: 1, activity: 'idle', lifecycle: 'active', terminalError: true },
    { workerId: 2, activity: 'in-flight', lifecycle: 'active', terminalError: false },
    { workerId: 3, activity: 'idle', lifecycle: 'active', terminalError: true },
  ]
  return [
    { workerId: 1, activity: 'in-flight', lifecycle: 'active', terminalError: false },
    { workerId: 2, activity: 'idle', lifecycle: 'draining', terminalError: false },
    { workerId: 3, activity: 'backoff', lifecycle: 'draining', terminalError: false },
  ]
}

function snapshotFor(state: SenderState): LoadgenSnapshot {
  const active = state !== 'idle' && state !== 'draining'
  const draining = state === 'draining'
  const retrying = state === 'retry-backoff'
  const terminalFailure = state === 'terminal-failure'
  const runState: LoadgenSnapshot['runState'] = state === 'idle'
    ? 'idle'
    : draining
      ? 'paused'
      : 'running'
  const workers = active || draining ? 3 : 2
  const attemptedTps = active || draining ? 1_200 : null
  const channel = {
    id: 'reader-to-throttler' as const, from: 'reader' as const, to: 'throttler' as const,
    capacity: numericControl(32, 'batches'), sentBatchesTotal: active ? 120 : 0,
    sentTransactionsTotal: active ? 12_000 : 0, receivedBatchesTotal: active ? 118 : 0,
    receivedTransactionsTotal: active ? 11_800 : 0, depthBatches: active ? 2 : 0,
    bufferedTransactions: active ? 200 : 0, handoffBatches: 0, handoffBatchesTotal: 0,
    blockedSenders: 0, oldestBlockedSenderMs: 0, inputBatchesPerSecond: active ? 12 : 0,
    outputBatchesPerSecond: active ? 12 : 0, inputTransactionsPerSecond: active ? 1_200 : 0,
    outputTransactionsPerSecond: active ? 1_200 : 0, inputTps: active ? 1_200 : 0,
    outputTps: active ? 1_200 : 0, throughputTps: active ? 1_200 : 0, blockedMs: null,
    trend: active ? 'steady' as const : 'unknown' as const, displayedPressure: active ? 0.2 : 0,
    flowState: active ? 'normal' as const : 'stopped' as const,
  }
  const retryPolicy = {
    maxAttempts: 4, backoffBaseMs: 250, backoffMultiplier: 2, jitterPercent: 10,
    retryableStatusCodes: [408, 429, 500, 502, 503, 504], retryTimeouts: true,
  }
  const sender = {
    id: 'sender' as const, workers: numericControl(workers, 'workers'), liveWorkers: active ? 3 : draining ? 1 : 2,
    drainingWorkers: draining ? 2 : 0,
    timeoutMs: numericControl(5_000, 'ms'), workerSlots: senderSlots(state),
    retryPolicy: retrying || terminalFailure ? retryPolicy : null, attemptedTps,
    retryAttemptedTps: retrying ? 240 : terminalFailure ? 120 : 0,
    terminalFailedTps: terminalFailure ? 40 : 0, inFlightRequests: active ? 2 : draining ? 1 : 0,
    attemptsStartedTotal: active || draining ? 12_000 : 0, retryAttemptsStartedTotal: retrying ? 1_200 : terminalFailure ? 600 : 0,
    successfulResponses: active || draining ? (terminalFailure ? 11_200 : 11_900) : 0,
    failedResponses: terminalFailure || retrying ? 800 : 0, retries: retrying || terminalFailure ? 600 : 0,
    timeoutsTotal: retrying ? 12 : terminalFailure ? 20 : 0,
    terminalFailedBatchesTotal: terminalFailure ? 40 : 0, terminalFailedTransactionsTotal: terminalFailure ? 400 : 0,
    ambiguousTimeoutTransactionsTotal: retrying ? 12 : 0, duplicateRiskTransactionsTotal: retrying ? 12 : 0,
    ambiguousTerminalTransactionsTotal: terminalFailure ? 40 : 0, state: runState,
  }
  const readerWorkers = numericControl(2, 'workers')
  const batchSize = numericControl(100, 'rows')
  const tps = numericControl(active || draining ? 120_000 : 0, 'tx/s')
  const mode = { applied: 'installed' as const, pending: null, applyMode: 'immediate' as const, writable: true, unavailableReason: null }
  return {
    revision: 1, adapterKind: 'simulation', connectionState: 'connected', runState,
    elapsedMs: active || draining ? 12_000 : 0,
    totalTransactions: active || draining ? 12_000 : 0, policy: null,
    reader: {
      id: 'reader', workers: readerWorkers, liveWorkers: 2, drainingWorkers: 0,
      workerSlots: null, readBatchSize: batchSize, readTps: active || draining ? 1_200 : null,
      configuredCapacityTps: 1_500, limitationReason: null, rowsRead: active || draining ? 12_000 : null,
      source: 'fixture source', sourceError: null, state: runState,
    },
    throttler: { id: 'throttler', requestedTps: tps, installationMode: mode, admittedTps: attemptedTps, limitedMs: null, state: runState },
    readerChannel: channel,
    senderChannel: { ...channel, id: 'throttler-to-sender' as const, from: 'throttler' as const, to: 'sender' as const },
    sender,
    http: {
      id: 'http', connectionState: 'connected', statusCode: terminalFailure ? 503 : 200,
      lastOutcome: terminalFailure ? 'http-response' : 'http-response', throughputTps: attemptedTps,
      inFlightRequests: sender.inFlightRequests, requestsStartedTotal: sender.attemptsStartedTotal,
      requestsCompletedTotal: active || draining ? 11_900 : 0, requestsSucceededTotal: sender.successfulResponses ?? 0,
      requestsFailedTotal: sender.failedResponses ?? 0, requestsTimedOutTotal: sender.timeoutsTotal,
      networkErrorsTotal: 0, latencyP95Ms: active || draining ? 42 : null,
    },
    target: {
      id: 'target', endpoint: 'fixture://target', acceptedTps: attemptedTps, rejectedTps: terminalFailure ? 40 : 0,
      latencyP95Ms: active || draining ? 42 : null, http200Responses: sender.successfulResponses,
      http503Responses: terminalFailure ? 40 : 0, connectionState: 'connected',
    },
  }
}

function SenderStory({ state }: { state: SenderState }) {
  const snapshot = snapshotFor(state)
  return (
    <PipelineSvg
      snapshot={snapshot}
      selectedId="sender"
      onSelect={noop}
      onChannelCapacityChange={noop}
      liveControls={liveControls(snapshot)}
    />
  )
}

const meta = { title: 'Pipeline/Sender', component: SenderStory } satisfies Meta<typeof SenderStory>
export default meta
type Story = StoryObj<typeof meta>

export const Idle: Story = { args: { state: 'idle' } }
export const Sending: Story = { args: { state: 'sending' } }
export const RetryBackoff: Story = { args: { state: 'retry-backoff' } }
export const TerminalFailure: Story = { args: { state: 'terminal-failure' } }
export const Draining: Story = { args: { state: 'draining' } }
