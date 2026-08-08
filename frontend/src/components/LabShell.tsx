import {
  FlaskConical,
  MousePointer2,
  Pause,
  Play,
  RotateCcw,
  X,
} from 'lucide-react'
import { useState } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import type {
  LoadgenSnapshot,
  QueueFlowState,
  QueueId,
  SelectableId,
} from '../model/loadgen'
import { useLoadgenSnapshot } from '../hooks/useLoadgenSnapshot'
import { getInspectorViewModel } from './inspectorViewModel'
import { NumericControl } from './NumericControl'
import { PipelineSvg } from './pipeline/PipelineSvg'
import type { WorkerActorId } from './pipeline/WorkerActor'

interface AdapterProps {
  adapter: LoadgenAdapter
}

interface SnapshotProps extends AdapterProps {
  snapshot: LoadgenSnapshot
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

function TopBar({ adapter, snapshot }: SnapshotProps) {
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
        onValueChange={(value) =>
          void adapter.dispatch({ type: 'set-requested-tps', value })
        }
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
}

function PipelineViewport({
  snapshot,
  selectedId,
  onSelect,
  onWorkerCountChange,
  onQueueCapacityChange,
}: PipelineViewportProps) {
  return (
    <main className="pipeline-scroll" aria-label="Pipeline viewport">
      <div className="pipeline-viewport" data-testid="pipeline-viewport">
        <PipelineSvg
          snapshot={snapshot}
          selectedId={selectedId}
          onSelect={onSelect}
          onWorkerCountChange={onWorkerCountChange}
          onQueueCapacityChange={onQueueCapacityChange}
        />
      </div>
    </main>
  )
}

interface InspectorDockProps extends SnapshotProps {
  selectedId: SelectableId | null
  onClearSelection: () => void
}

function InspectorControls({
  adapter,
  snapshot,
  selectedId,
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
            onValueChange={(value) =>
              void adapter.dispatch({ type: 'set-requested-tps', value })
            }
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

interface WorkspaceProps extends SnapshotProps {
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onClearSelection: () => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onQueueCapacityChange: (queue: QueueId, value: number) => void
}

function Workspace({
  adapter,
  snapshot,
  selectedId,
  onSelect,
  onClearSelection,
  onWorkerCountChange,
  onQueueCapacityChange,
}: WorkspaceProps) {
  return (
    <div className="workspace">
      <PipelineViewport
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
        onQueueCapacityChange={onQueueCapacityChange}
      />
      <InspectorDock
        adapter={adapter}
        snapshot={snapshot}
        selectedId={selectedId}
        onClearSelection={onClearSelection}
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
  const [selectedId, setSelectedId] = useState<SelectableId | null>(null)
  const handleWorkerCountChange = (actor: WorkerActorId, value: number) => {
    void adapter.dispatch({ type: 'set-worker-count', actor, value })
  }
  const handleQueueCapacityChange = (queue: QueueId, value: number) => {
    void adapter.dispatch({ type: 'set-queue-capacity', queue, value })
  }

  return (
    <section className="lab-shell" aria-label="Load generator laboratory">
      <TopBar adapter={adapter} snapshot={snapshot} />
      <Workspace
        adapter={adapter}
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onClearSelection={() => setSelectedId(null)}
        onWorkerCountChange={handleWorkerCountChange}
        onQueueCapacityChange={handleQueueCapacityChange}
      />
      <QueueStateLegend />
    </section>
  )
}
