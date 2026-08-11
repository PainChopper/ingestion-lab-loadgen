import type { SelectableId, SenderSnapshot } from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor, type WorkerActorId } from './WorkerActor'

interface SenderActorProps {
  snapshot: SenderSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  geometry: PipelineGeometry['actors']['sender']
  orientation: PipelineOrientation
}

export function SenderActor({
  snapshot,
  selected,
  onSelect,
  onWorkerCountChange,
  geometry,
  orientation,
}: SenderActorProps) {
  const workerSummary =
    `${formatInteger(snapshot.workerStates.inFlight)} in-flight · ` +
    `${formatInteger(snapshot.workerStates.backoff)} backoff`

  return (
    <WorkerActor
      actor="sender"
      title="SENDER"
      titlePoint={geometry.title}
      titleTone="sender"
      bounds={geometry.bounds}
      controls={geometry.controls}
      workers={snapshot.workers}
      workerStates={snapshot.workerStates}
      workerSlots={snapshot.workerSlots}
      runState={snapshot.state}
      inputPort={geometry.ports.input}
      outputPort={geometry.ports.output}
      primaryMetric={formatRate(snapshot.attemptedTps)}
      secondaryMetric={workerSummary}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      onWorkerCountChange={onWorkerCountChange}
    />
  )
}
