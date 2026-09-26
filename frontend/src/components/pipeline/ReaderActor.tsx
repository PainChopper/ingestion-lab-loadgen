import type { ReaderSnapshot, SelectableId } from '../../model/loadgen'
import { formatInteger, formatRate } from './formatters'
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
          value: snapshot.sourceError !== null ? '1 error' : '— errors',
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
      activityCounts={{ idle: snapshot.idleWorkers, reading: snapshot.readingWorkers, blocked: snapshot.blockedWorkers, 'in-flight': 0, backoff: 0 }}
      drainingActivityCounts={{ idle: snapshot.drainingIdleWorkers, reading: snapshot.drainingReadingWorkers, blocked: snapshot.drainingBlockedWorkers, 'in-flight': 0, backoff: 0 }}
      runState={snapshot.state}
      active={rateDriven
        ? snapshot.readTps !== null && snapshot.readTps > 0
        : undefined}
      outputPort={geometry.ports.output}
      primaryMetric={`Read ${formatRate(snapshot.readTps)}`}
      statusMetricSegments={workerStateSegments}
      metricPoints={geometry.metrics}
      orientation={orientation}
      selected={selected}
      onSelect={onSelect}
      desiredControl={desiredControl}
      muted={snapshot.sourceError != null}
      sourceErrorOperation={snapshot.sourceError?.operation}
    />
  )
}
