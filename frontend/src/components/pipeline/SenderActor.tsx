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
  const inFlight =
    snapshot.inFlightRequests === null
      ? '—'
      : `${formatInteger(snapshot.inFlightRequests)} in-flight`

  return (
    <WorkerActor
      actor="sender"
      title="SENDER"
      titlePoint={geometry.title}
      titleTone="sender"
      bounds={geometry.bounds}
      controls={geometry.controls}
      workers={snapshot.workers}
      runState={snapshot.state}
      inputPort={geometry.ports.input}
      outputPort={geometry.ports.output}
      primaryMetric={formatRate(snapshot.attemptedTps)}
      secondaryMetric={inFlight}
      metricPoints={'metrics' in geometry ? geometry.metrics : undefined}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      onWorkerCountChange={onWorkerCountChange}
    />
  )
}
