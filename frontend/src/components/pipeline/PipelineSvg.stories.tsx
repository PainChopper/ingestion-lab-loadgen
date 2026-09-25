import type { Meta, StoryObj } from '@storybook/react-vite'
import type { LoadgenSnapshot, NumericControlSnapshot } from '../../model/loadgen'
import type { LiveControls } from '../../hooks/useDesiredControl'
import { PipelineSvg } from './PipelineSvg'
import './PipelineSvg.css'

const noop = () => {}
const accept = async () => true

function numericControl(applied: number, unit = 'items'): NumericControlSnapshot {
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

function workerSlots(count: number, activity: 'idle' | 'reading' | 'in-flight' | 'backoff' | 'blocked') {
  return Array.from({ length: count }, (_, index) => ({
    workerId: index + 1,
    activity,
    lifecycle: 'active' as const,
    source: 'fixture',
    terminalError: false,
  }))
}

function snapshotFor(
  runState: LoadgenSnapshot['runState'],
  flowState: LoadgenSnapshot['readerChannel']['flowState'],
  pressure: number,
): LoadgenSnapshot {
  const active = runState === 'running'
  const blocked = flowState === 'backpressure'
  const workers = active ? 3 : 0
  const channel = (id: LoadgenSnapshot['readerChannel']['id'], from: 'reader' | 'throttler', to: 'throttler' | 'sender') => ({
    id,
    from,
    to,
    capacity: numericControl(blocked ? 8 : 32, 'batches'),
    sentBatchesTotal: active ? 120 : 0,
    sentTransactionsTotal: active ? 12_000 : 0,
    receivedBatchesTotal: active ? (blocked ? 72 : 118) : 0,
    receivedTransactionsTotal: active ? (blocked ? 7_200 : 11_800) : 0,
    depthBatches: active ? (blocked ? 8 : 2) : 0,
    bufferedTransactions: active ? (blocked ? 800 : 200) : 0,
    handoffBatches: active && blocked ? 4 : 0,
    handoffBatchesTotal: active && blocked ? 18 : 0,
    blockedSenders: active && blocked ? 2 : 0,
    oldestBlockedSenderMs: active && blocked ? 1_250 : 0,
    inputBatchesPerSecond: active ? 12 : 0,
    outputBatchesPerSecond: active ? (blocked ? 7 : 12) : 0,
    inputTransactionsPerSecond: active ? 1_200 : 0,
    outputTransactionsPerSecond: active ? (blocked ? 700 : 1_200) : 0,
    inputTps: active ? 1_200 : 0,
    outputTps: active ? (blocked ? 700 : 1_200) : 0,
    throughputTps: active ? (blocked ? 700 : 1_200) : 0,
    blockedMs: active && blocked ? 1_250 : null,
    trend: blocked ? 'rising' as const : active ? 'steady' as const : 'unknown' as const,
    displayedPressure: pressure,
    flowState,
  })

  const workerControl = numericControl(workers, 'workers')
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
    connectionState: 'connected',
    runState,
    elapsedMs: active ? 12_000 : 0,
    totalTransactions: active ? 12_000 : 0,
    policy: null,
    reader: {
      id: 'reader', workers: workerControl, liveWorkers: workers, drainingWorkers: 0,
      workerSlots: workerSlots(workers, active ? (blocked ? 'blocked' : 'reading') : 'idle') as LoadgenSnapshot['reader']['workerSlots'],
      readBatchSize: batchControl, readTps: active ? 1_200 : 0, configuredCapacityTps: null,
      limitationReason: blocked ? 'downstream-backpressure' : null, rowsRead: active ? 12_000 : 0,
      source: 'fixture source', sourceError: null, state: runState,
    },
    throttler: {
      id: 'throttler', requestedTps: tpsControl, installationMode: modeControl,
      admittedTps: active ? (blocked ? 700 : 1_200) : 0, limitedMs: blocked ? 1_250 : null, state: runState,
    },
    readerChannel: channel('reader-to-throttler', 'reader', 'throttler'),
    senderChannel: channel('throttler-to-sender', 'throttler', 'sender'),
    sender: {
      id: 'sender', workers: workerControl, liveWorkers: workers, drainingWorkers: 0,
      timeoutMs: numericControl(5_000, 'ms'), workerSlots: workerSlots(workers, active ? (blocked ? 'backoff' : 'in-flight') : 'idle') as LoadgenSnapshot['sender']['workerSlots'],
      retryPolicy: null, attemptedTps: active ? 1_200 : 0, retryAttemptedTps: 0, terminalFailedTps: 0,
      inFlightRequests: active ? 3 : 0, attemptsStartedTotal: active ? 12_000 : 0, retryAttemptsStartedTotal: 0,
      successfulResponses: active ? 11_900 : 0, failedResponses: 0, retries: 0, timeoutsTotal: 0,
      terminalFailedBatchesTotal: 0, terminalFailedTransactionsTotal: 0, ambiguousTimeoutTransactionsTotal: 0,
      duplicateRiskTransactionsTotal: 0, ambiguousTerminalTransactionsTotal: 0, state: runState,
    },
    http: {
      id: 'http', connectionState: 'connected', statusCode: 200, lastOutcome: 'http-response',
      throughputTps: active ? 1_200 : 0, inFlightRequests: active ? 3 : 0, requestsStartedTotal: active ? 12_000 : 0,
      requestsCompletedTotal: active ? 11_900 : 0, requestsSucceededTotal: active ? 11_900 : 0,
      requestsFailedTotal: 0, requestsTimedOutTotal: 0, networkErrorsTotal: 0, latencyP95Ms: active ? 42 : null,
    },
    target: {
      id: 'target', endpoint: 'fixture://target', acceptedTps: active ? 1_200 : 0, rejectedTps: 0,
      latencyP95Ms: active ? 42 : null, http200Responses: active ? 11_900 : 0, http503Responses: 0,
      connectionState: 'connected',
    },
  }
}

function PipelineStory({ snapshot }: { snapshot: LoadgenSnapshot }) {
  return (
    <PipelineSvg
      snapshot={snapshot}
      selectedId={null}
      onSelect={noop}
      onChannelCapacityChange={noop}
      liveControls={liveControls(snapshot)}
    />
  )
}

const meta = {
  title: 'Pipeline/Full pipeline',
  component: PipelineStory,
} satisfies Meta<typeof PipelineStory>

export default meta
type Story = StoryObj<typeof meta>

export const Idle: Story = { args: { snapshot: snapshotFor('idle', 'stopped', 0) } }
export const Running: Story = { args: { snapshot: snapshotFor('running', 'normal', 0.2) } }
export const Paused: Story = { args: { snapshot: snapshotFor('paused', 'stopped', 0.35) } }
export const Backpressure: Story = { args: { snapshot: snapshotFor('running', 'backpressure', 1) } }
