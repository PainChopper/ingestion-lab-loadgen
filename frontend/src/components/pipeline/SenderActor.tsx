import type { SelectableId, RunState, SenderSnapshot } from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor } from './WorkerActor'
import type { DesiredControl } from '../../hooks/useDesiredControl'

interface SenderActorProps {
  snapshot: SenderSnapshot
  runState: RunState | null
  outputTps: number | null
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
  geometry: PipelineGeometry['actors']['sender']
  orientation: PipelineOrientation
}

export function SenderActor({
  snapshot,
  runState,
  outputTps,
  selected,
  onSelect,
  desiredControl,
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
      activityCounts={{
        idle: snapshot.idleWorkers,
        reading: 0,
        blocked: 0,
        'in-flight': snapshot.inFlightWorkers,
        backoff: snapshot.backoffWorkers,
      }}
      drainingActivityCounts={{
        idle: snapshot.drainingIdleWorkers,
        reading: 0,
        blocked: 0,
        'in-flight': snapshot.drainingInFlightWorkers,
        backoff: snapshot.drainingBackoffWorkers,
      }}
      runState={runState}
      inputPort={geometry.ports.input}
      outputPort={geometry.ports.output}
      primaryMetric={formatRate(outputTps)}
      statusMetricSegments={workerStateSegments}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
    />
  )
}
