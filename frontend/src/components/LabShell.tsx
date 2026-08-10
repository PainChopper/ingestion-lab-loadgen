import {
  FlaskConical,
  MousePointer2,
  Pause,
  Play,
  RotateCcw,
  X,
} from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import type {
  LoadgenSnapshot,
  QueueFlowState,
  QueueId,
  SelectableId,
  ThrottlerInstallationMode,
} from '../model/loadgen'
import { useLoadgenSnapshot } from '../hooks/useLoadgenSnapshot'
import { getInspectorViewModel } from './inspectorViewModel'
import { NumericControl } from './NumericControl'
import { PipelineSvg } from './pipeline/PipelineSvg'
import type { WorkerActorId } from './pipeline/WorkerActor'
import { createPipelineGeometry } from './pipeline/geometry'
import {
  usePipelineOrientation,
  type PipelineOrientation,
} from './pipeline/pipelineLayout'
import { normalizedWorkerCount } from './pipeline/workerActorLayout'

interface AdapterProps {
  adapter: LoadgenAdapter
}

interface SnapshotProps extends AdapterProps {
  snapshot: LoadgenSnapshot
}

interface RequestedTpsControlProps {
  requestedTpsPreview: number | null
  onRequestedTpsPreviewChange: (value: number | null) => void
  onRequestedTpsChange: (value: number) => Promise<boolean>
}

const QUEUE_STATES: ReadonlyArray<{
  state: QueueFlowState
  label: string
}> = [
  { state: 'normal', label: 'Normal flow' },
  { state: 'near-limit', label: 'Near limit' },
  { state: 'backpressure', label: 'Backpressure' },
  { state: 'stopped', label: 'Stopped' },
  { state: 'connection-error', label: 'Connection error' },
]

function formatDuration(elapsedMs: number): string {
  const totalSeconds = Math.floor(elapsedMs / 1_000)
  const hours = Math.floor(totalSeconds / 3_600)
  const minutes = Math.floor((totalSeconds % 3_600) / 60)
  const seconds = totalSeconds % 60
  return [hours, minutes, seconds]
    .map((part) => String(part).padStart(2, '0'))
    .join(':')
}

function formatCount(value: number): string {
  return new Intl.NumberFormat('en-US').format(value)
}

function TopBar({
  adapter,
  snapshot,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
}: SnapshotProps & RequestedTpsControlProps) {
  const running = snapshot.runState === 'running'

  return (
    <header className="topbar">
      <div className="brand">
        <FlaskConical aria-hidden="true" />
        <span>Go Load Generator Lab</span>
      </div>

      <div
        id="run-state"
        className={`run-state run-state--${snapshot.runState}`}
        aria-live="polite"
      >
        <span className="state-dot" aria-hidden="true" />
        <span>{snapshot.runState}</span>
      </div>

      <div className="run-actions">
        <button
          id="run-toggle"
          className="button button--primary"
          type="button"
          onClick={() =>
            void adapter.dispatch({ type: running ? 'pause' : 'run' })
          }
          title={running ? 'Pause run' : 'Start run'}
          aria-label={running ? 'Pause run' : 'Start run'}
        >
          {running ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
          <span>{running ? 'Pause' : 'Run'}</span>
        </button>
        <button
          id="reset-run"
          className="button button--icon"
          type="button"
          onClick={() => void adapter.dispatch({ type: 'reset' })}
          title="Reset run"
          aria-label="Reset run"
        >
          <RotateCcw aria-hidden="true" />
        </button>
      </div>

      <dl className="run-counters">
        <div>
          <dt>Elapsed</dt>
          <dd>{formatDuration(snapshot.elapsedMs)}</dd>
        </div>
        <div>
          <dt>Total</dt>
          <dd>{formatCount(snapshot.totalTransactions)}</dd>
        </div>
      </dl>

      <NumericControl
        className="numeric-control--topbar"
        label="Requested TPS"
        control={snapshot.throttler.requestedTps}
        onPreviewChange={onRequestedTpsPreviewChange}
        onValueChange={(value) => void onRequestedTpsChange(value)}
      />
    </header>
  )
}

interface PipelineViewportProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onQueueCapacityChange: (queue: QueueId, value: number) => void
  requestedTpsPreview: number | null
  onRequestedTpsPreviewChange: (value: number | null) => void
  onRequestedTpsChange: (value: number) => Promise<boolean>
  onInstallationModeChange: (
    value: ThrottlerInstallationMode,
  ) => Promise<boolean>
  orientation: PipelineOrientation
}

function PipelineViewport({
  snapshot,
  selectedId,
  onSelect,
  onWorkerCountChange,
  onQueueCapacityChange,
  requestedTpsPreview,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
  onInstallationModeChange,
  orientation,
}: PipelineViewportProps) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const [landscapeContentWidth, setLandscapeContentWidth] = useState(1120)

  useEffect(() => {
    if (orientation !== 'landscape') return
    const viewport = viewportRef.current
    if (viewport === null) return
    const updateWidth = (width: number) => {
      const resolved = Math.max(1120, Math.round(width))
      setLandscapeContentWidth((current) =>
        current === resolved ? current : resolved
      )
    }

    updateWidth(viewport.clientWidth)
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0]
      if (entry !== undefined) updateWidth(entry.contentRect.width)
    })
    observer.observe(viewport, { box: 'content-box' })
    return () => observer.disconnect()
  }, [orientation])

  const geometry = useMemo(() => createPipelineGeometry({
    orientation,
    landscapeContentWidth,
    readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
    senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
  }), [
    landscapeContentWidth,
    orientation,
    snapshot.reader.workers,
    snapshot.sender.workers,
  ])

  return (
    <main className="pipeline-scroll" aria-label="Pipeline viewport">
      <div
        ref={viewportRef}
        className={`pipeline-viewport pipeline-viewport--${orientation}`}
        data-testid="pipeline-viewport"
        data-layout={orientation}
        data-content-width={geometry.viewBox.width}
        style={orientation === 'portrait'
          ? { aspectRatio: `480 / ${geometry.viewBox.height}` }
          : undefined}
      >
        <PipelineSvg
          snapshot={snapshot}
          selectedId={selectedId}
          onSelect={onSelect}
          onWorkerCountChange={onWorkerCountChange}
          onQueueCapacityChange={onQueueCapacityChange}
          requestedTpsPreview={requestedTpsPreview}
          onRequestedTpsPreviewChange={onRequestedTpsPreviewChange}
          onRequestedTpsChange={onRequestedTpsChange}
          onInstallationModeChange={onInstallationModeChange}
          orientation={orientation}
          geometry={geometry}
        />
      </div>
    </main>
  )
}

interface InspectorDockProps extends SnapshotProps, RequestedTpsControlProps {
  selectedId: SelectableId | null
  onClearSelection: () => void
}

function InspectorControls({
  adapter,
  snapshot,
  selectedId,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
}: Omit<InspectorDockProps, 'onClearSelection'>) {
  switch (selectedId) {
    case 'reader':
      return (
        <div className="inspector-controls" aria-label="Reader configuration">
          <NumericControl
            label="Workers"
            control={snapshot.reader.workers}
            onValueChange={(value) =>
              void adapter.dispatch({
                type: 'set-worker-count',
                actor: 'reader',
                value,
              })
            }
          />
          <NumericControl
            label="Read batch size"
            control={snapshot.reader.readBatchSize}
            onValueChange={(value) =>
              void adapter.dispatch({ type: 'set-read-batch-size', value })
            }
          />
        </div>
      )
    case 'throttler': {
      const running = snapshot.throttler.state === 'running'
      return (
        <div className="inspector-controls" aria-label="Throttler configuration">
          <NumericControl
            label="Requested TPS"
            control={snapshot.throttler.requestedTps}
            onPreviewChange={onRequestedTpsPreviewChange}
            onValueChange={(value) => void onRequestedTpsChange(value)}
          />
          <button
            className="button inspector-command"
            type="button"
            onClick={() =>
              void adapter.dispatch({ type: running ? 'pause' : 'run' })
            }
          >
            {running ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
            <span>{running ? 'Pause' : 'Resume'}</span>
          </button>
        </div>
      )
    }
    case 'sender':
      return (
        <div className="inspector-controls" aria-label="Sender configuration">
          <NumericControl
            label="Workers"
            control={snapshot.sender.workers}
            onValueChange={(value) =>
              void adapter.dispatch({
                type: 'set-worker-count',
                actor: 'sender',
                value,
              })
            }
          />
          <NumericControl
            label="HTTP batch size"
            control={snapshot.sender.httpBatchSize}
            onValueChange={(value) =>
              void adapter.dispatch({ type: 'set-http-batch-size', value })
            }
          />
          <NumericControl
            label="HTTP timeout"
            control={snapshot.sender.timeoutMs}
            onValueChange={(valueMs) =>
              void adapter.dispatch({ type: 'set-http-timeout', valueMs })
            }
          />
        </div>
      )
    case 'target':
      return (
        <div className="inspector-controls" aria-label="Target configuration">
          <NumericControl
            label="Artificial delay"
            control={snapshot.target.artificialDelayMs}
            onValueChange={(valueMs) =>
              void adapter.dispatch({ type: 'set-target-delay', valueMs })
            }
          />
          <NumericControl
            label="503 error rate"
            control={snapshot.target.errorRatePercent}
            onValueChange={(valuePercent) =>
              void adapter.dispatch({
                type: 'set-target-error-rate',
                valuePercent,
              })
            }
          />
        </div>
      )
    default:
      return null
  }
}

function InspectorDock({
  adapter,
  snapshot,
  selectedId,
  onClearSelection,
  requestedTpsPreview,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
}: InspectorDockProps) {
  const model = getInspectorViewModel(snapshot, selectedId)

  return (
    <aside className="inspector" aria-labelledby="inspector-title">
      <div className="inspector-head">
        <div>
          <h2 id="inspector-title">{model?.title ?? 'INSPECTOR'}</h2>
          <p>{model?.kind ?? 'No selection'}</p>
        </div>
        <div className="inspector-head-actions">
          <span className="adapter-badge">{snapshot.adapterKind}</span>
          {model !== null && (
            <button
              className="inspector-close"
              type="button"
              onClick={onClearSelection}
              title="Clear selection"
              aria-label="Clear selection"
            >
              <X aria-hidden="true" />
            </button>
          )}
        </div>
      </div>
      {model === null ? (
        <div className="inspector-empty">
          <MousePointer2 aria-hidden="true" />
          <span>No pipeline object selected</span>
        </div>
      ) : (
        <div className="inspector-content">
          <InspectorControls
            adapter={adapter}
            snapshot={snapshot}
            selectedId={selectedId}
            requestedTpsPreview={requestedTpsPreview}
            onRequestedTpsPreviewChange={onRequestedTpsPreviewChange}
            onRequestedTpsChange={onRequestedTpsChange}
          />
          <dl className="inspector-data">
            {model.rows.map((row) => (
              <div key={row.label}>
                <dt>{row.label}</dt>
                <dd>{row.value}</dd>
              </div>
            ))}
          </dl>
        </div>
      )}
    </aside>
  )
}

interface WorkspaceProps extends SnapshotProps, RequestedTpsControlProps {
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onClearSelection: () => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onQueueCapacityChange: (queue: QueueId, value: number) => void
  onInstallationModeChange: (
    value: ThrottlerInstallationMode,
  ) => Promise<boolean>
  orientation: PipelineOrientation
}

function Workspace({
  adapter,
  snapshot,
  selectedId,
  onSelect,
  onClearSelection,
  onWorkerCountChange,
  onQueueCapacityChange,
  requestedTpsPreview,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
  onInstallationModeChange,
  orientation,
}: WorkspaceProps) {
  return (
    <div className="workspace">
      <PipelineViewport
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
        onQueueCapacityChange={onQueueCapacityChange}
        requestedTpsPreview={requestedTpsPreview}
        onRequestedTpsPreviewChange={onRequestedTpsPreviewChange}
        onRequestedTpsChange={onRequestedTpsChange}
        onInstallationModeChange={onInstallationModeChange}
        orientation={orientation}
      />
      <InspectorDock
        adapter={adapter}
        snapshot={snapshot}
        selectedId={selectedId}
        onClearSelection={onClearSelection}
        requestedTpsPreview={requestedTpsPreview}
        onRequestedTpsPreviewChange={onRequestedTpsPreviewChange}
        onRequestedTpsChange={onRequestedTpsChange}
      />
    </div>
  )
}

function QueueStateLegend() {
  return (
    <footer className="queue-legend" aria-label="Queue states">
      <strong>QUEUE STATES</strong>
      <div className="queue-legend-items">
        {QUEUE_STATES.map(({ state, label }) => (
          <span key={state} className="legend-item">
            <i className={`legend-swatch legend-swatch--${state}`} />
            {label}
          </span>
        ))}
      </div>
    </footer>
  )
}

export function LabShell({ adapter }: AdapterProps) {
  const snapshot = useLoadgenSnapshot(adapter)
  const orientation = usePipelineOrientation()
  const [selectedId, setSelectedId] = useState<SelectableId | null>(null)
  const [requestedTpsDraft, setRequestedTpsDraft] = useState<{
    value: number | null
    resolutionRevision: number | null
  }>({ value: null, resolutionRevision: null })
  const requestedTpsPreview = requestedTpsDraft.value

  const handleRequestedTpsPreviewChange = (value: number | null) => {
    setRequestedTpsDraft({ value, resolutionRevision: null })
  }

  useEffect(() => {
    if (requestedTpsPreview === null) return
    const control = snapshot.throttler.requestedTps
    if (
      control.applied === requestedTpsPreview ||
      control.pending === requestedTpsPreview ||
      control.preview === requestedTpsPreview ||
      (
        requestedTpsDraft.resolutionRevision !== null &&
        snapshot.revision >= requestedTpsDraft.resolutionRevision
      )
    ) {
      setRequestedTpsDraft({ value: null, resolutionRevision: null })
    }
  }, [requestedTpsDraft, requestedTpsPreview, snapshot.revision, snapshot.throttler.requestedTps])

  const handleRequestedTpsChange = async (value: number): Promise<boolean> => {
    setRequestedTpsDraft({ value, resolutionRevision: null })
    try {
      const receipt = await adapter.dispatch({ type: 'set-requested-tps', value })
      setRequestedTpsDraft((current) => {
        if (current.value !== value) return current
        return receipt.accepted
          ? { value, resolutionRevision: receipt.snapshotRevision }
          : { value: null, resolutionRevision: null }
      })
      return receipt.accepted
    } catch {
      setRequestedTpsDraft({ value: null, resolutionRevision: null })
      return false
    }
  }
  const handleInstallationModeChange = async (
    value: ThrottlerInstallationMode,
  ): Promise<boolean> => {
    try {
      const receipt = await adapter.dispatch({
        type: 'set-throttler-installation-mode',
        value,
      })
      return receipt.accepted
    } catch {
      return false
    }
  }
  const handleWorkerCountChange = (actor: WorkerActorId, value: number) => {
    void adapter.dispatch({ type: 'set-worker-count', actor, value })
  }
  const handleQueueCapacityChange = (queue: QueueId, value: number) => {
    void adapter.dispatch({ type: 'set-queue-capacity', queue, value })
  }

  return (
    <section
      className={`lab-shell lab-shell--${orientation}`}
      aria-label="Load generator laboratory"
      data-layout={orientation}
    >
      <TopBar
        adapter={adapter}
        snapshot={snapshot}
        requestedTpsPreview={requestedTpsPreview}
        onRequestedTpsPreviewChange={handleRequestedTpsPreviewChange}
        onRequestedTpsChange={handleRequestedTpsChange}
      />
      <Workspace
        adapter={adapter}
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onClearSelection={() => setSelectedId(null)}
        onWorkerCountChange={handleWorkerCountChange}
        onQueueCapacityChange={handleQueueCapacityChange}
        requestedTpsPreview={requestedTpsPreview}
        onRequestedTpsPreviewChange={handleRequestedTpsPreviewChange}
        onRequestedTpsChange={handleRequestedTpsChange}
        onInstallationModeChange={handleInstallationModeChange}
        orientation={orientation}
      />
      <QueueStateLegend />
    </section>
  )
}
