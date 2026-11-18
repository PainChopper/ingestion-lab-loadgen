import { clsx } from 'clsx'
import type { CSSProperties, KeyboardEvent } from 'react'
import type { ChannelSnapshot, SelectableId } from '../../model/loadgen'
import { channelPressureColor } from '../../model/channelFlowState'
import { formatInteger, formatMilliseconds, formatRate } from './formatters'
import type { PipelineChannelGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import {
  type Box,
  type CapacityValues,
  getChannelCapacityGeometryPresentation,
} from './channelCableGeometry'

interface ChannelCableProps {
  snapshot: ChannelSnapshot
  batchSize: ChannelSnapshot['capacity']
  geometry: PipelineChannelGeometry
  selected: boolean
  onSelect: (id: SelectableId) => void
  orientation?: PipelineOrientation
  capacityValues?: CapacityValues
  hoseForbiddenBoxes?: readonly Box[]
}

export function ChannelCable({
  snapshot,
  batchSize,
  geometry,
  selected,
  onSelect,
  orientation = 'landscape',
  capacityValues,
  hoseForbiddenBoxes,
}: ChannelCableProps) {
  const { start, end, metrics } = geometry
  const presentation = getChannelCapacityGeometryPresentation(
    snapshot.capacity,
    start,
    end,
    orientation,
    capacityValues,
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
  const batchStrokeWidth =
    2.5 +
    7.5 *
      (batchSize.applied === null || batchSize.max <= batchSize.min
        ? 0
        : Math.min(
            1,
            Math.max(0, (batchSize.applied - batchSize.min) / (batchSize.max - batchSize.min)),
          ))

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
        Depth {formatInteger(snapshot.depthBatches)} / {formatInteger(snapshot.capacity.applied)}{' '}
        batches
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
