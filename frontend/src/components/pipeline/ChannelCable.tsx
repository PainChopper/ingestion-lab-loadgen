import { clsx } from 'clsx'
import type { CSSProperties, KeyboardEvent } from 'react'
import type { ChannelSnapshot, SelectableId } from '../../model/loadgen'
import { channelPressureColor } from '../../model/channelFlowState'
import { formatInteger, formatMilliseconds, formatRate } from './formatters'
import type { PipelineChannelGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { type Box, getChannelCapacityGeometryPresentation } from './channelCableGeometry'

interface ChannelCableProps {
  snapshot: ChannelSnapshot
  batchSize: ChannelSnapshot['capacity']
  geometry: PipelineChannelGeometry
  selected: boolean
  onSelect: (id: SelectableId) => void
  orientation?: PipelineOrientation
  hoseForbiddenBoxes?: readonly Box[]
}

export function ChannelCable({
  snapshot,
  batchSize,
  geometry,
  selected,
  onSelect,
  orientation = 'landscape',
  hoseForbiddenBoxes,
}: ChannelCableProps) {
  const { start, end, metrics } = geometry
  const presentation = getChannelCapacityGeometryPresentation(
    snapshot.capacity,
    start,
    end,
    orientation,
    hoseForbiddenBoxes,
  )
  const portrait = orientation === 'portrait'
  const flowActive =
    (snapshot.flowState === 'normal' ||
      snapshot.flowState === 'near-limit' ||
      snapshot.flowState === 'backpressure') &&
    (snapshot.inputTransactionsPerSecond > 0 || snapshot.outputTransactionsPerSecond > 0)
  const markersVisible =
    snapshot.inputTransactionsPerSecond > 0 || snapshot.outputTransactionsPerSecond > 0
  const channelStyle = {
    '--pipeline-channel-pressure-color': channelPressureColor(snapshot.displayedPressure),
  } as CSSProperties
  // Presentation scale: 1,000 tx is the narrow hose, 100,000 tx the widest.
  // These visual anchors are independent of the operator's control budgets.
  const batchStrokeWidth =
    2.5 + 7.5 * Math.min(1, Math.max(0, ((batchSize ?? 1_000) - 1_000) / 99_000))

  const handleSelectionKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  return (
    <g
      id={`channel-${snapshot.id}`}
      className={clsx(
        'pipeline-channel pipeline-selectable',
        `pipeline-channel--${snapshot.flowState}`,
        markersVisible && 'pipeline-channel--markers-visible',
        flowActive && 'pipeline-channel--flow-active',
        selected && 'pipeline-selectable--selected',
      )}
      role="button"
      tabIndex={0}
      aria-label={`Inspect ${snapshot.from} to ${snapshot.to} channel`}
      aria-pressed={selected}
      data-pressure={snapshot.displayedPressure.toFixed(2)}
      data-input-active={snapshot.inputTransactionsPerSecond > 0}
      data-output-active={snapshot.outputTransactionsPerSecond > 0}
      data-leg-count={presentation.legCount}
      style={channelStyle}
      onClick={() => onSelect(snapshot.id)}
      onKeyDown={handleSelectionKeyDown}
    >
      <path d={presentation.cablePath} className="pipeline-link-hit-area" aria-hidden="true" />

      <path
        d={presentation.cablePath}
        className="pipeline-channel-cable"
        style={{ strokeWidth: batchStrokeWidth }}
      />
      <text
        x={metrics.x}
        y={metrics.throughputY}
        textAnchor="middle"
        className="pipeline-small-strong pipeline-channel-metric"
      >
        {formatRate(snapshot.throughputTps)}
      </text>
      <text
        x={metrics.x}
        y={metrics.depthY}
        textAnchor="middle"
        className="pipeline-small pipeline-channel-metric"
      >
        Depth {formatInteger(snapshot.depthBatches)} / {formatInteger(snapshot.capacity)} batches
      </text>
      {snapshot.blockedSenders > 0 && (
        <text
          x={metrics.x}
          y={metrics.waitingY}
          textAnchor="middle"
          className="pipeline-channel-wait-status"
        >
          {portrait ? (
            <>
              <tspan x={metrics.x}>Waiting upstream {formatInteger(snapshot.blockedSenders)}</tspan>
              <tspan x={metrics.x} dy="16">
                oldest {formatMilliseconds(snapshot.oldestBlockedSenderMs)}
              </tspan>
            </>
          ) : (
            `Waiting upstream ${formatInteger(snapshot.blockedSenders)}, oldest ${formatMilliseconds(snapshot.oldestBlockedSenderMs)}`
          )}
        </text>
      )}
    </g>
  )
}
