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
  ChannelFlowState,
  ChannelId,
  SelectableId,
  ThrottlerInstallationMode,
} from '../model/loadgen'
import { useLoadgenSnapshot } from '../hooks/useLoadgenSnapshot'
import {
  useDesiredControl,
  type LiveControls,
} from '../hooks/useDesiredControl'
import {
  getInspectorViewModel,
  type InspectorRow,
} from './inspectorViewModel'
import { NumericControl } from './NumericControl'
import { PipelineSvg } from './pipeline/PipelineSvg'
import { createPipelineGeometry } from './pipeline/geometry'
import {
  usePipelineOrientation,
  type PipelineOrientation,
} from './pipeline/pipelineLayout'

interface AdapterProps {
  adapter: LoadgenAdapter
}

interface SnapshotProps extends AdapterProps {
  snapshot: LoadgenSnapshot
}

const CHANNEL_STATES: ReadonlyArray<{
  state: ChannelFlowState
  label: string
}> = [
  { state: 'normal', label: 'Normal flow' },
  { state: 'near-limit', label: 'Near limit' },
  { state: 'backpressure', label: 'Backpressure' },
  { state: 'stopped', label: 'Stopped' },
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
  frozenObserved,
}: SnapshotProps & { frozenObserved: boolean }) {
  const running = snapshot.runState === 'running'
  const paused = snapshot.runState === 'paused'
  const faulted = snapshot.runState === 'faulted'
  const runUnavailable = snapshot.adapterKind === 'http' &&
    (snapshot.connectionState !== 'connected' || snapshot.policy === null)

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
          disabled={runUnavailable || faulted}
          title={running ? 'Pause run' : paused ? 'Resume run' : 'Start run'}
          aria-label={running ? 'Pause run' : paused ? 'Resume run' : 'Start run'}
        >
          {running ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
          <span>{running ? 'Pause' : paused ? 'Resume' : 'Run'}</span>
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

      {frozenObserved && (
        <p className="run-state-qualifier" role="status">
          Paused — last observed telemetry frozen; controls remain desired
        </p>
      )}

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

    </header>
  )
}

interface PipelineViewportProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onChannelCapacityChange: (channel: ChannelId, value: number) => void
  liveControls: LiveControls
  frozenObserved: boolean
  orientation: PipelineOrientation
}

function PipelineViewport({
  snapshot,
  selectedId,
  onSelect,
  onChannelCapacityChange,
  liveControls,
  frozenObserved,
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
    readerWorkers: liveControls.readerWorkers.desired,
    senderWorkers: liveControls.senderWorkers.desired,
  }), [
    landscapeContentWidth,
    orientation,
    liveControls.readerWorkers.desired,
    liveControls.senderWorkers.desired,
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
          onChannelCapacityChange={onChannelCapacityChange}
          liveControls={liveControls}
          frozenObserved={frozenObserved}
          orientation={orientation}
          geometry={geometry}
        />
      </div>
    </main>
  )
}

interface InspectorDockProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onClearSelection: () => void
  liveControls: LiveControls
  frozenObserved: boolean
}

function InspectorControls({
  snapshot,
  selectedId,
  liveControls,
}: Omit<InspectorDockProps, 'onClearSelection' | 'frozenObserved'>) {
  switch (selectedId) {
    case 'reader':
      return (
        <div className="inspector-controls" aria-label="Reader configuration">
          <NumericControl
            label="Workers"
            control={snapshot.reader.workers}
            desiredControl={liveControls.readerWorkers}
          />
          <NumericControl
            label="Read batch size"
            control={snapshot.reader.readBatchSize}
            desiredControl={liveControls.readBatchSize}
          />
        </div>
      )
    case 'throttler': {
      return (
        <div className="inspector-controls" aria-label="Throttler configuration">
          <NumericControl
            label="Requested TPS"
            control={snapshot.throttler.requestedTps}
            desiredControl={liveControls.requestedTps}
          />
          <label className="inspector-select-control">
            <span>Valve mode</span>
            <select
              value={liveControls.installationMode.desired}
              disabled={!liveControls.installationMode.available || liveControls.installationMode.phase === 'pending'}
              onChange={(event) => {
                liveControls.installationMode.preview(
                  event.currentTarget.value as ThrottlerInstallationMode,
                )
                void liveControls.installationMode.commit()
              }}
            >
              <option value="installed">Installed</option>
              <option value="bypass">Bypassed</option>
            </select>
          </label>
        </div>
      )
    }
    case 'sender':
      return (
        <div className="inspector-controls" aria-label="Sender configuration">
          <NumericControl
            label="Workers"
            control={snapshot.sender.workers}
            desiredControl={liveControls.senderWorkers}
          />
        </div>
      )
    case 'target':
      return null
    default:
      return null
  }
}

function InspectorDock({
  snapshot,
  selectedId,
  onClearSelection,
  liveControls,
  frozenObserved,
}: InspectorDockProps) {
  const model = getInspectorViewModel(snapshot, selectedId, frozenObserved)
  const controls = model?.id === 'sender'
    ? (
      <section className="inspector-section" aria-labelledby="sender-controls-title">
        <h3 id="sender-controls-title" className="inspector-section__title">Управление</h3>
        <InspectorControls
          snapshot={snapshot}
          selectedId={selectedId}
          liveControls={liveControls}
        />
      </section>
    )
    : (
        <InspectorControls
          snapshot={snapshot}
        selectedId={selectedId}
        liveControls={liveControls}
      />
    )

  return (
    <aside className="inspector" aria-labelledby="inspector-title">
      <div className="inspector-head">
        <div>
          <h2 id="inspector-title">{model?.title ?? 'INSPECTOR'}</h2>
          <p>{model?.kind ?? 'No selection'}</p>
        </div>
        <div className="inspector-head-actions">
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
          {controls}
          {model.sections === undefined ? (
            <dl className="inspector-data">
              {model.rows.map((row) => <InspectorDataRow key={row.key ?? row.label} row={row} />)}
            </dl>
          ) : model.sections.map((section) => (
            <section className="inspector-section" key={section.title} aria-labelledby={`sender-${section.title}`}>
              <h3 id={`sender-${section.title}`} className="inspector-section__title">{section.title}</h3>
              <dl
                className="inspector-data inspector-data--sender"
                data-testid="sender-inspector-section-data"
              >
                {section.rows.map((row) => <InspectorDataRow key={row.label} row={row} />)}
              </dl>
            </section>
          ))}
        </div>
      )}
    </aside>
  )
}

function InspectorDataRow({ row }: { row: InspectorRow }) {
  const className = [
    row.layout === 'full-width' ? 'inspector-data__row--full-width' : null,
    row.segments === undefined ? null : 'inspector-data__row--worker-states',
	row.kind === 'source-error' ? 'inspector-data__row--source-error' : null,
  ].filter(Boolean).join(' ')

  return (
    <div className={className || undefined}>
      {row.kind === 'source-error'
        ? <span className="inspector-data__source-error-label" data-testid="reader-source-error-label">SOURCE ERROR</span>
        : <dt>{row.label}</dt>}
      <dd>
        {row.segments === undefined
          ? row.disclosureValue === undefined
            ? row.value
            : <details className="inspector-disclosure">
                <summary>{row.value}</summary>
                <span>{row.disclosureLabel}</span>
                <code>{row.disclosureValue}</code>
              </details>
          : row.segments.map((segment, index) => (
            <span className={`worker-state worker-state--${segment.tone}`} key={segment.value}>
              {index > 0 && <span aria-hidden="true">· </span>}{segment.value}
            </span>
          ))}
      </dd>
    </div>
  )
}

interface WorkspaceProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onClearSelection: () => void
  onChannelCapacityChange: (channel: ChannelId, value: number) => void
  liveControls: LiveControls
  frozenObserved: boolean
  orientation: PipelineOrientation
}

function Workspace({
  snapshot,
  selectedId,
  onSelect,
  onClearSelection,
  onChannelCapacityChange,
  liveControls,
  frozenObserved,
  orientation,
}: WorkspaceProps) {
  return (
    <div className="workspace">
      <PipelineViewport
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={onSelect}
        onChannelCapacityChange={onChannelCapacityChange}
        liveControls={liveControls}
        frozenObserved={frozenObserved}
        orientation={orientation}
      />
      <InspectorDock
        snapshot={snapshot}
        selectedId={selectedId}
        onClearSelection={onClearSelection}
        liveControls={liveControls}
        frozenObserved={frozenObserved}
      />
    </div>
  )
}

function ChannelStateLegend() {
  return (
    <footer className="channel-legend" aria-label="Channel states">
      <strong>CHANNEL STATES</strong>
      <div className="channel-legend-items">
        {CHANNEL_STATES.map(({ state, label }) => (
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
  const { snapshot, liveSnapshot, frozenObserved } = useLoadgenSnapshot(adapter)
  const orientation = usePipelineOrientation()
  const [selectedId, setSelectedId] = useState<SelectableId | null>(null)
  const immediate = (control: { applied: number | null; applyMode: string }) =>
    control.applied !== null && control.applyMode === 'immediate'
  const readerWorkers = useDesiredControl({
    applied: liveSnapshot.reader.workers.applied ?? liveSnapshot.reader.workers.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.reader.workers),
    dispatch: (value) => adapter.dispatch({
      type: 'set-worker-count', actor: 'reader', value,
    }),
  })
  const readBatchSize = useDesiredControl({
    applied: liveSnapshot.reader.readBatchSize.applied ?? liveSnapshot.reader.readBatchSize.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.reader.readBatchSize),
    dispatch: (value) => adapter.dispatch({ type: 'set-read-batch-size', value }),
  })
  const requestedTps = useDesiredControl({
    applied: liveSnapshot.throttler.requestedTps.applied ?? liveSnapshot.throttler.requestedTps.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.throttler.requestedTps),
    dispatch: (value) => adapter.dispatch({ type: 'set-requested-tps', value }),
  })
  const installationMode = useDesiredControl<ThrottlerInstallationMode>({
    applied: liveSnapshot.throttler.installationMode.applied ?? 'installed',
    revision: liveSnapshot.revision,
    available: liveSnapshot.throttler.installationMode.applied !== null &&
      liveSnapshot.throttler.installationMode.writable &&
      liveSnapshot.throttler.installationMode.applyMode === 'immediate',
    dispatch: (value) => adapter.dispatch({
      type: 'set-throttler-installation-mode', value,
    }),
    rejectionMessage: 'Valve mode change rejected',
    unavailableMessage: 'Valve mode change unavailable',
  })
  const senderWorkers = useDesiredControl({
    applied: liveSnapshot.sender.workers.applied ?? liveSnapshot.sender.workers.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.sender.workers),
    dispatch: (value) => adapter.dispatch({ type: 'set-sender-workers', value }),
  })
  const timeoutMs = useDesiredControl({
    applied: liveSnapshot.sender.timeoutMs.applied ?? liveSnapshot.sender.timeoutMs.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.sender.timeoutMs),
    dispatch: (valueMs) => adapter.dispatch({ type: 'set-http-timeout', valueMs }),
  })
  const liveControls: LiveControls = {
    readerWorkers,
    readBatchSize,
    requestedTps,
    installationMode,
    senderWorkers,
    timeoutMs,
  }
  const handleChannelCapacityChange = (channel: ChannelId, value: number) => {
    void adapter.dispatch(
      channel === 'reader-to-throttler'
        ? { type: 'set-reader-channel-capacity', value }
        : { type: 'set-sender-channel-capacity', value },
    )
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
        frozenObserved={frozenObserved}
      />
      <Workspace
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onClearSelection={() => setSelectedId(null)}
        onChannelCapacityChange={handleChannelCapacityChange}
        liveControls={liveControls}
        frozenObserved={frozenObserved}
        orientation={orientation}
      />
      <ChannelStateLegend />
    </section>
  )
}
