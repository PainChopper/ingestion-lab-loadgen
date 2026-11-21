import { workerStateCounts } from '../workerStateCounts'
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
  const counts = workerStateCounts(snapshot.workerStates)
  const workerStateSegments = [
    {
      value: `${formatInteger(counts.idleWorkers)} idle`,
      tone: 'idle' as const,
    },
    {
      value: `${formatInteger(counts.inFlightWorkers)} in-flight`,
      tone: 'in-flight' as const,
    },
    {
      value: `${formatInteger(counts.backoffWorkers)} backoff`,
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
      liveWorkers={counts.liveWorkers}
      drainingWorkers={counts.drainingWorkers}
      activityCounts={{
        idle: counts.idleWorkers,
        reading: 0,
        blocked: 0,
        'in-flight': counts.inFlightWorkers,
        backoff: counts.backoffWorkers,
      }}
      drainingActivityCounts={{
        idle: counts.drainingIdleWorkers,
        reading: 0,
        blocked: 0,
        'in-flight': counts.drainingInFlightWorkers,
        backoff: counts.drainingBackoffWorkers,
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
