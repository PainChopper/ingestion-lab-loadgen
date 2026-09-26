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
  const workerStateSegments = [
        {
          value: `${formatInteger(snapshot.idleWorkers)} idle`,
          tone: 'idle' as const,
        },
        {
          value: `${formatInteger(snapshot.inFlightWorkers)} in-flight`,
          tone: 'in-flight' as const,
        },
        {
          value: `${formatInteger(snapshot.backoffWorkers)} backoff`,
          tone: 'backoff' as const,
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
      activityCounts={{ idle: snapshot.idleWorkers, reading: 0, blocked: 0, 'in-flight': snapshot.inFlightWorkers, backoff: snapshot.backoffWorkers }}
      drainingActivityCounts={{ idle: snapshot.drainingIdleWorkers, reading: 0, blocked: 0, 'in-flight': snapshot.drainingInFlightWorkers, backoff: snapshot.drainingBackoffWorkers }}
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
