import type { SelectableId, SenderSnapshot } from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor } from './WorkerActor'
import type { DesiredControl } from '../../hooks/useDesiredControl'

interface SenderActorProps {
  snapshot: SenderSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
  frozenObserved: boolean
  geometry: PipelineGeometry['actors']['sender']
  orientation: PipelineOrientation
}

export function SenderActor({
  snapshot,
  selected,
  onSelect,
  desiredControl,
  frozenObserved: _frozenObserved,
  geometry,
  orientation,
}: SenderActorProps) {
  const workerStateSegments = snapshot.workerSlots === null
    ? [{ value: 'Worker state telemetry unavailable', tone: 'idle' as const }]
    : [
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'idle').length)} idle`,
          tone: 'idle' as const,
        },
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'in-flight').length)} in-flight`,
          tone: 'in-flight' as const,
        },
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'backoff').length)} backoff`,
          tone: 'backoff' as const,
        },
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.terminalError).length)} errors`,
          tone: 'error' as const,
        },
      ]

  return (
    <WorkerActor
      actor="sender"
      title="SENDER"
      titlePoint={geometry.title}
      titleTone="sender"
      bounds={geometry.bounds}
      controls={geometry.controls}
      workers={snapshot.workers}
      liveWorkers={snapshot.liveWorkers}
      drainingWorkers={snapshot.drainingWorkers}
      workerSlots={snapshot.workerSlots}
      runState={snapshot.state}
      inputPort={geometry.ports.input}
      outputPort={geometry.ports.output}
      primaryMetric={formatRate(snapshot.attemptedTps)}
      statusMetricSegments={workerStateSegments}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
    />
  )
}
