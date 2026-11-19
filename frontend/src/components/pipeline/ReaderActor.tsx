import type { ReaderSnapshot, SelectableId, RunState } from '../../model/loadgen'
import { formatInteger, formatRate, formatReaderErrorCount } from './formatters'
import type { PipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { WorkerActor } from './WorkerActor'
import type { DesiredControl } from '../../hooks/useDesiredControl'

interface ReaderActorProps {
  snapshot: ReaderSnapshot
  rateDriven: boolean
  runState: RunState | null
  selected: boolean
  onSelect: (id: SelectableId) => void
  desiredControl: DesiredControl<number>
  geometry: PipelineGeometry['actors']['reader']
  orientation: PipelineOrientation
}

export function ReaderActor({
  snapshot,
  runState,
  rateDriven,
  selected,
  onSelect,
  desiredControl,
  geometry,
  orientation,
}: ReaderActorProps) {
  const { sourceError } = snapshot
  const hasSourceError = sourceError !== null && sourceError !== undefined
  const workerStateSegments = [
    {
      value: `${formatInteger(snapshot.idleWorkers)} idle`,
      tone: 'idle' as const,
    },
    {
      value: `${formatInteger(snapshot.readingWorkers)} reading`,
      tone: 'in-flight' as const,
    },
    {
      value: `${formatInteger(snapshot.blockedWorkers)} blocked`,
      tone: 'backoff' as const,
    },
    {
      value: formatReaderErrorCount(snapshot),
      tone: 'error' as const,
    },
  ]

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
      activityCounts={{
        idle: snapshot.idleWorkers,
        reading: snapshot.readingWorkers,
        blocked: snapshot.blockedWorkers,
        'in-flight': 0,
        backoff: 0,
      }}
      drainingActivityCounts={{
        idle: snapshot.drainingIdleWorkers,
        reading: snapshot.drainingReadingWorkers,
        blocked: snapshot.drainingBlockedWorkers,
        'in-flight': 0,
        backoff: 0,
      }}
      runState={runState}
      active={rateDriven ? snapshot.readTps !== null && snapshot.readTps > 0 : undefined}
      outputPort={geometry.ports.output}
      primaryMetric={`Read ${formatRate(snapshot.readTps)}`}
      statusMetricSegments={workerStateSegments}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
      muted={hasSourceError}
      sourceErrorOperation={sourceError?.operation}
    />
  )
}
