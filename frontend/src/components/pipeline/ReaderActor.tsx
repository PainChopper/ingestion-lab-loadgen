import type { ReaderSnapshot, SelectableId } from '../../model/loadgen'
import { formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor, type WorkerActorId } from './WorkerActor'

interface ReaderActorProps {
  snapshot: ReaderSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  geometry: PipelineGeometry['actors']['reader']
  orientation: PipelineOrientation
}

export function ReaderActor({
  snapshot,
  selected,
  onSelect,
  onWorkerCountChange,
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
      runState={snapshot.state}
      outputPort={geometry.ports.output}
      primaryMetric={`Read ${formatRate(snapshot.readTps)}`}
      secondaryMetric={`Capacity ${formatRate(snapshot.configuredCapacityTps)}`}
      statusMetric={snapshot.limitationReason === 'downstream-backpressure'
        ? 'Downstream limited'
        : undefined}
      metricPoints={'metrics' in geometry ? geometry.metrics : undefined}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      onWorkerCountChange={onWorkerCountChange}
    />
  )
}
