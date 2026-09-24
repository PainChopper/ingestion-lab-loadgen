import type { ReaderSnapshot, SelectableId } from '../../model/loadgen'
import { formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor } from './WorkerActor'
import type { DesiredControl } from '../../hooks/useDesiredControl'

interface ReaderActorProps {
  snapshot: ReaderSnapshot
  rateDriven: boolean
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
  frozenObserved: boolean
  geometry: PipelineGeometry['actors']['reader']
  orientation: PipelineOrientation
}

export function ReaderActor({
  snapshot,
  rateDriven,
  selected,
  onSelect,
  desiredControl,
  frozenObserved: _frozenObserved,
  geometry,
  orientation,
}: ReaderActorProps) {
  return (
    <WorkerActor
      actor="reader"
      title="READER"
      titlePoint={geometry.title}
      titleTone="reader"
      bounds={geometry.bounds}
      controls={geometry.controls}
      workers={snapshot.workers}
      liveWorkers={snapshot.liveWorkers}
      drainingWorkers={snapshot.drainingWorkers}
      workerSlots={snapshot.workerSlots}
      runState={snapshot.state}
      active={rateDriven
        ? snapshot.readTps !== null && snapshot.readTps > 0
        : undefined}
      outputPort={geometry.ports.output}
      primaryMetric={`Read ${formatRate(snapshot.readTps)}`}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
    />
  )
}
