import type {
  LoadgenSnapshot,
  QueueId,
  SelectableId,
} from '../../model/loadgen'
import { HttpLink } from './HttpLink'
import { PIPELINE_VIEW_BOX_VALUE } from './geometry'
import { ACTOR_GEOMETRY } from './geometry'
import { QueueCable } from './QueueCable'
import { ReaderActor } from './ReaderActor'
import { SenderActor } from './SenderActor'
import { TargetActor } from './TargetActor'
import { ThrottlerActor } from './ThrottlerActor'
import type { WorkerActorId } from './WorkerActor'
import './PipelineSvg.css'

interface PipelineSvgProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onQueueCapacityChange: (queue: QueueId, value: number) => void
}

export function PipelineSvg({
  snapshot,
  selectedId,
  onSelect,
  onWorkerCountChange,
  onQueueCapacityChange,
}: PipelineSvgProps) {
  return (
    <svg
      className="pipeline-svg"
      viewBox={PIPELINE_VIEW_BOX_VALUE}
      preserveAspectRatio="xMinYMin meet"
      role="group"
      aria-label="Reader to target load generation pipeline"
    >
      <QueueCable
        snapshot={snapshot.queue1}
        start={ACTOR_GEOMETRY.reader.ports.output}
        end={ACTOR_GEOMETRY.throttler.ports.input}
        selected={selectedId === snapshot.queue1.id}
        onSelect={onSelect}
        onCapacityChange={onQueueCapacityChange}
      />
      <QueueCable
        snapshot={snapshot.queue2}
        start={ACTOR_GEOMETRY.throttler.ports.output}
        end={ACTOR_GEOMETRY.sender.ports.input}
        selected={selectedId === snapshot.queue2.id}
        onSelect={onSelect}
        onCapacityChange={onQueueCapacityChange}
      />
      <ReaderActor
        snapshot={snapshot.reader}
        selected={selectedId === snapshot.reader.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
      />
      <ThrottlerActor
        snapshot={snapshot.throttler}
        selected={selectedId === snapshot.throttler.id}
        onSelect={onSelect}
      />
      <SenderActor
        snapshot={snapshot.sender}
        selected={selectedId === snapshot.sender.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
      />
      <HttpLink
        snapshot={snapshot.http}
        selected={selectedId === snapshot.http.id}
        onSelect={onSelect}
      />
      <TargetActor
        snapshot={snapshot.target}
        selected={selectedId === snapshot.target.id}
        onSelect={onSelect}
      />
    </svg>
  )
}
