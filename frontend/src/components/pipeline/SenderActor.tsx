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
  const workerStateSummary = snapshot.workerSlots === null
    ? 'Worker state telemetry unavailable'
    : `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'idle').length)} idle · ` +
      `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'in-flight').length)} in-flight · ` +
      `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'backoff').length)} backoff · ` +
      `${formatInteger(snapshot.workerSlots.filter((slot) => slot.terminalError).length)} errors`

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
      statusMetric={workerStateSummary}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
    />
  )
}
