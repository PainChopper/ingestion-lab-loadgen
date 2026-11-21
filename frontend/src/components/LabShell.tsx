import { FlaskConical, MousePointer2, X } from 'lucide-react'
import { clsx } from 'clsx'
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import type { LoadgenSnapshot, ChannelFlowState, SelectableId } from '../model/loadgen'
import { useLoadgenSnapshot } from '../hooks/useLoadgenSnapshot'
import { useDesiredControl, type LiveControls } from '../hooks/useDesiredControl'
import { getInspectorViewModel, type InspectorRow } from './inspectorViewModel'
import { NumericControl } from './NumericControl'
import { formatInteger } from './pipeline/formatters'
import { PipelineSvg } from './pipeline/PipelineSvg'
import { createPipelineGeometry } from './pipeline/geometry'
import {
  PIPELINE_LANDSCAPE_MIN_WIDTH,
  usePipelineLayout,
  type PipelineInspectorLayout,
  type PipelineOrientation,
} from './pipeline/pipelineLayout'

interface AdapterProps {
  adapter: LoadgenAdapter
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
  return [hours, minutes, seconds].map((part) => String(part).padStart(2, '0')).join(':')
}

function TopBar({ snapshot }: { snapshot: LoadgenSnapshot }) {
  const state =
    snapshot.connectionState === 'connected'
      ? (snapshot.runState ?? snapshot.connectionState)
      : snapshot.connectionState
  return (
    <header className="topbar">
      <div className="brand">
        <FlaskConical aria-hidden="true" />
        <span>Go Load Generator Lab</span>
      </div>

      <div id="run-state" className={`run-state run-state--${state}`} aria-live="polite">
        <span className="state-dot" aria-hidden="true" />
        <span>{state}</span>
      </div>

      <dl className="run-counters">
        <div>
          <dt>Elapsed</dt>
          <dd>{formatDuration(snapshot.elapsedMs)}</dd>
        </div>
        <div>
          <dt>Total</dt>
          <dd>{formatInteger(snapshot.totalTransactions)}</dd>
        </div>
      </dl>
    </header>
  )
}

interface PipelineViewportProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  liveControls: LiveControls
  orientation: PipelineOrientation
}

function PipelineViewport({
  snapshot,
  selectedId,
  onSelect,
  liveControls,
  orientation,
}: PipelineViewportProps) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const [landscapeContentWidth, setLandscapeContentWidth] = useState(PIPELINE_LANDSCAPE_MIN_WIDTH)

  useLayoutEffect(() => {
    if (orientation !== 'landscape') return
    const viewport = viewportRef.current
    if (viewport === null) return
    const updateWidth = (width: number) => {
      const resolved = Math.max(PIPELINE_LANDSCAPE_MIN_WIDTH, Math.round(width))
      setLandscapeContentWidth((current) => (current === resolved ? current : resolved))
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

  const geometry = useMemo(
    () =>
      createPipelineGeometry({
        orientation,
        landscapeContentWidth,
        readerWorkers: Math.max(
          liveControls.readerWorkers.desired,
          snapshot.reader.workerStates.length,
        ),
        senderWorkers: Math.max(
          liveControls.senderWorkers.desired,
          snapshot.sender.workerStates.length,
        ),
      }),
    [
      landscapeContentWidth,
      orientation,
      liveControls.readerWorkers.desired,
      liveControls.senderWorkers.desired,
      snapshot.reader.workerStates.length,
      snapshot.sender.workerStates.length,
    ],
  )

  return (
    <main className="pipeline-scroll" aria-label="Pipeline viewport">
      <div
        ref={viewportRef}
        className={`pipeline-viewport pipeline-viewport--${orientation}`}
        data-testid="pipeline-viewport"
        data-layout={orientation}
        data-content-width={geometry.viewBox.width}
        style={
          orientation === 'portrait'
            ? { aspectRatio: `480 / ${geometry.viewBox.height}` }
            : { height: geometry.viewBox.height }
        }
      >
        <PipelineSvg
          snapshot={snapshot}
          selectedId={selectedId}
          onSelect={onSelect}
          liveControls={liveControls}
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
}

function InspectorControls({
  snapshot,
  selectedId,
  liveControls,
}: Omit<InspectorDockProps, 'onClearSelection'>) {
  switch (selectedId) {
    case 'reader':
      return (
        <div className="inspector-controls" aria-label="Reader configuration">
          <NumericControl
            label="Workers"
            control={snapshot.reader.workers}
            desiredControl={liveControls.readerWorkers}
          />
          <p>Read batch size: {formatInteger(snapshot.reader.readBatchSize)} tx</p>
        </div>
      )
    case 'throttler':
      return (
        <div className="inspector-controls" aria-label="Throttler configuration">
          <NumericControl
            label="Requested TPS"
            control={snapshot.throttler.requestedTps}
            desiredControl={liveControls.requestedTps}
          />
          <label className="inspector-select-control">
            <span>Throttler valve installed</span>
            <input
              type="checkbox"
              checked={liveControls.installed.desired}
              disabled={
                !liveControls.installed.available || liveControls.installed.phase === 'pending'
              }
              onChange={(event) => {
                liveControls.installed.preview(event.currentTarget.checked)
                void liveControls.installed.commit()
              }}
            />
          </label>
        </div>
      )
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
    default:
      return null
  }
}

function InspectorDock({
  snapshot,
  selectedId,
  onClearSelection,
  liveControls,
}: InspectorDockProps) {
  const inspectorId = useId()
  const titleId = `${inspectorId}-title`
  const model = getInspectorViewModel(snapshot, selectedId)

  return (
    <aside className="inspector" aria-labelledby={titleId}>
      <div className="inspector-head">
        <div>
          <h2 id={titleId}>{model?.title ?? 'INSPECTOR'}</h2>
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
          <InspectorControls
            snapshot={snapshot}
            selectedId={selectedId}
            liveControls={liveControls}
          />
          <dl className="inspector-data">
            {model.rows.map((row) => (
              <InspectorDataRow key={row.key ?? row.label} row={row} />
            ))}
          </dl>
        </div>
      )}
    </aside>
  )
}

function InspectorDataRow({ row }: { row: InspectorRow }) {
  const className = clsx(
    row.layout === 'full-width' && 'inspector-data__row--full-width',
    row.segments !== undefined && 'inspector-data__row--worker-states',
    row.kind === 'source-error' && 'inspector-data__row--source-error',
  )

  return (
    <div className={className || undefined}>
      {row.kind === 'source-error' ? (
        <span
          className="inspector-data__source-error-label"
          data-testid="reader-source-error-label"
        >
          SOURCE ERROR
        </span>
      ) : (
        <dt>{row.label}</dt>
      )}
      <dd>
        {row.segments === undefined
          ? row.value
          : row.segments.map((segment, index) => (
              <span className={`worker-state worker-state--${segment.tone}`} key={segment.value}>
                {index > 0 && <span aria-hidden="true">· </span>}
                {segment.value}
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
  liveControls: LiveControls
  orientation: PipelineOrientation
  inspectorLayout: PipelineInspectorLayout
}

function Workspace({
  snapshot,
  selectedId,
  onSelect,
  onClearSelection,
  liveControls,
  orientation,
  inspectorLayout,
}: WorkspaceProps) {
  const workspaceRef = useRef<HTMLDivElement>(null)
  const isDrawer = inspectorLayout === 'drawer'
  const drawerOpen = isDrawer && selectedId !== null
  const closeInspector = useCallback(() => {
    const workspace = workspaceRef.current
    const active = document.activeElement
    if (
      isDrawer &&
      (active === document.body || workspace?.querySelector('.inspector')?.contains(active))
    ) {
      workspace
        ?.querySelector<SVGElement>('.pipeline-selectable--selected[role="button"]')
        ?.focus({ preventScroll: true })
    }
    onClearSelection()
  }, [isDrawer, onClearSelection])

  useEffect(() => {
    if (!drawerOpen) return
    let closeTimer: number | undefined
    const onEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.repeat) return
      window.clearTimeout(closeTimer)
      // Wait until the input and pointer gesture handlers have processed Escape.
      closeTimer = window.setTimeout(() => {
        if (!event.defaultPrevented) closeInspector()
      }, 0)
    }
    window.addEventListener('keydown', onEscape, true)
    return () => {
      window.removeEventListener('keydown', onEscape, true)
      window.clearTimeout(closeTimer)
    }
  }, [closeInspector, drawerOpen])

  return (
    <div ref={workspaceRef} className="workspace" data-inspector-layout={inspectorLayout}>
      <PipelineViewport
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={onSelect}
        liveControls={liveControls}
        orientation={orientation}
      />
      {(!isDrawer || drawerOpen) && (
        <InspectorDock
          snapshot={snapshot}
          selectedId={selectedId}
          onClearSelection={closeInspector}
          liveControls={liveControls}
        />
      )}
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
            <i className={`legend-swatch legend-swatch--${state}`} aria-hidden="true" />
            {label}
          </span>
        ))}
      </div>
    </footer>
  )
}

export function LabShell({ adapter }: AdapterProps) {
  const { snapshot, liveSnapshot } = useLoadgenSnapshot(adapter)
  const { orientation, inspectorLayout, toggleSelection } = usePipelineLayout()
  const [selectedId, setSelectedId] = useState<SelectableId | null>(null)
  const clearSelection = useCallback(() => setSelectedId(null), [])
  const select = (id: SelectableId) =>
    setSelectedId((current) => (toggleSelection && current === id ? null : id))
  const immediate = (control: { applied: number | null; applyMode: string }) =>
    control.applied !== null && control.applyMode === 'immediate'
  const readerWorkers = useDesiredControl({
    applied: liveSnapshot.reader.workers.applied ?? liveSnapshot.reader.workers.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.reader.workers),
    dispatch: (value) =>
      adapter.dispatch({
        type: 'set-reader-workers',
        value,
      }),
  })
  const requestedTps = useDesiredControl({
    applied: liveSnapshot.throttler.requestedTps.applied ?? liveSnapshot.throttler.requestedTps.min,
    revision: liveSnapshot.revision,
    available: immediate(liveSnapshot.throttler.requestedTps),
    dispatch: (value) => adapter.dispatch({ type: 'set-requested-tps', value }),
  })
  const installed = useDesiredControl<boolean>({
    applied: liveSnapshot.throttler.installed.applied ?? true,
    revision: liveSnapshot.revision,
    available:
      liveSnapshot.throttler.installed.applied !== null &&
      liveSnapshot.throttler.installed.applyMode === 'immediate',
    dispatch: (value) =>
      adapter.dispatch({
        type: 'set-throttler-installed',
        value,
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
  const liveControls: LiveControls = {
    readerWorkers,
    requestedTps,
    installed,
    senderWorkers,
  }

  return (
    <div
      className={`lab-shell lab-shell--${orientation}`}
      data-layout={orientation}
      style={
        { '--pipeline-landscape-min-width': `${PIPELINE_LANDSCAPE_MIN_WIDTH}px` } as CSSProperties
      }
    >
      <TopBar snapshot={snapshot} />
      <Workspace
        snapshot={snapshot}
        selectedId={selectedId}
        onSelect={select}
        onClearSelection={clearSelection}
        liveControls={liveControls}
        orientation={orientation}
        inspectorLayout={inspectorLayout}
      />
      <ChannelStateLegend />
    </div>
  )
}
