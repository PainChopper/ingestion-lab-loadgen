import type {
  LoadgenSnapshot,
  QueueId,
  SelectableId,
} from '../../model/loadgen'
import { HttpLink } from './HttpLink'
import { PipelineMarkers } from './PipelineMarkers'
import {
  PIPELINE_VIEW_BOX_VALUE,
  QUEUE_CABLE_ENDPOINTS,
} from './geometry'
import { QueueCable } from './QueueCable'
import { ReaderActor } from './ReaderActor'
import { SenderActor } from './SenderActor'
import { TargetActor } from './TargetActor'
import { ThrottlerActor } from './ThrottlerActor'
import { VALVE_APERTURE } from './throttlerValve'
import { usePipelineMarkerLifecycle } from './usePipelineMarkerLifecycle'
import type { WorkerActorId } from './WorkerActor'
import './PipelineSvg.css'

interface PipelineSvgProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onQueueCapacityChange: (queue: QueueId, value: number) => void
  requestedTpsPreview: number | null
  onRequestedTpsPreviewChange: (value: number | null) => void
  onRequestedTpsChange: (value: number) => Promise<boolean>
}

export function PipelineSvg({
  snapshot,
  selectedId,
  onSelect,
  onWorkerCountChange,
  onQueueCapacityChange,
  requestedTpsPreview,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
}: PipelineSvgProps) {
  const markers = usePipelineMarkerLifecycle(snapshot)
  const queue1Endpoints = QUEUE_CABLE_ENDPOINTS[snapshot.queue1.id]
  const queue2Endpoints = QUEUE_CABLE_ENDPOINTS[snapshot.queue2.id]

  return (
    <svg
      className="pipeline-svg"
      viewBox={PIPELINE_VIEW_BOX_VALUE}
      preserveAspectRatio="xMinYMin meet"
      role="group"
      aria-label="Reader to target load generation pipeline"
    >
      <HttpLink
        snapshot={snapshot.http}
        selected={selectedId === snapshot.http.id}
        onSelect={onSelect}
      />
      <QueueCable
        snapshot={snapshot.queue1}
        start={queue1Endpoints.start}
        end={queue1Endpoints.end}
        selected={selectedId === snapshot.queue1.id}
        onSelect={onSelect}
        onCapacityChange={onQueueCapacityChange}
      />
      <QueueCable
        snapshot={snapshot.queue2}
        start={queue2Endpoints.start}
        end={queue2Endpoints.end}
        selected={selectedId === snapshot.queue2.id}
        onSelect={onSelect}
        onCapacityChange={onQueueCapacityChange}
      />
      <ellipse
        cx={VALVE_APERTURE.centerX}
        cy={VALVE_APERTURE.centerY}
        rx={VALVE_APERTURE.radiusX}
        ry={VALVE_APERTURE.radiusY}
        fill="#03111f"
        className="pipeline-valve-vacuum"
        data-vacuum-fill="uniform"
      />
      <PipelineMarkers snapshot={snapshot} markers={markers} />
      <ReaderActor
        snapshot={snapshot.reader}
        selected={selectedId === snapshot.reader.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
      />
      <ThrottlerActor
        snapshot={snapshot.throttler}
        upstreamQueue={snapshot.queue1}
        previewTps={requestedTpsPreview}
        selected={selectedId === snapshot.throttler.id}
        onSelect={onSelect}
        onPreviewTpsChange={onRequestedTpsPreviewChange}
        onRequestedTpsChange={onRequestedTpsChange}
      />
      <SenderActor
        snapshot={snapshot.sender}
        selected={selectedId === snapshot.sender.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
      />
      <TargetActor
        snapshot={snapshot.target}
        selected={selectedId === snapshot.target.id}
        onSelect={onSelect}
      />
    </svg>
  )
}
