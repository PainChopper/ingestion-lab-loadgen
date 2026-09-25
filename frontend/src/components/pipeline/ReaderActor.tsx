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
  const workerStateSegments = snapshot.workerSlots === null
    ? [{ value: 'Worker state telemetry unavailable', tone: 'idle' as const }]
    : [
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'idle').length)} idle`,
          tone: 'idle' as const,
        },
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'reading').length)} reading`,
          tone: 'in-flight' as const,
        },
        {
          value: `${formatInteger(snapshot.workerSlots.filter((slot) => slot.activity === 'blocked').length)} blocked`,
          tone: 'backoff' as const,
        },
        {
          value: snapshot.sourceError !== null && snapshot.sourceError.workerId !== null
            ? '1 error'
            : '— errors',
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
      workerSlots={snapshot.workerSlots}
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
