import type {
  LoadgenSnapshot,
  ChannelId,
  SelectableId,
  ThrottlerInstallationMode,
} from '../../model/loadgen'
import { useMemo } from 'react'
import { HttpLink } from './HttpLink'
import { PipelineMarkers } from './PipelineMarkers'
import {
  createPipelineGeometry,
  type PipelineGeometry,
} from './geometry'
import { ChannelCable } from './ChannelCable'
import { ReaderActor } from './ReaderActor'
import { SenderActor } from './SenderActor'
import { TargetActor } from './TargetActor'
import { ThrottlerActor } from './ThrottlerActor'
import { VALVE_APERTURE } from './throttlerValve'
import { usePipelineMarkerLifecycle } from './usePipelineMarkerLifecycle'
import type { WorkerActorId } from './WorkerActor'
import type { PipelineOrientation } from './pipelineLayout'
import { normalizedWorkerCount } from './workerActorLayout'
import './PipelineSvg.css'

interface PipelineSvgProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onWorkerCountChange: (actor: WorkerActorId, value: number) => void
  onChannelCapacityChange: (channel: ChannelId, value: number) => void
  requestedTpsPreview: number | null
  onRequestedTpsPreviewChange: (value: number | null) => void
  onRequestedTpsChange: (value: number) => Promise<boolean>
  onInstallationModeChange: (
    value: ThrottlerInstallationMode,
  ) => Promise<boolean>
  orientation?: PipelineOrientation
  geometry?: PipelineGeometry
}

export function PipelineSvg({
  snapshot,
  selectedId,
  onSelect,
  onWorkerCountChange,
  onChannelCapacityChange,
  requestedTpsPreview,
  onRequestedTpsPreviewChange,
  onRequestedTpsChange,
  onInstallationModeChange,
  orientation = 'landscape',
  geometry,
}: PipelineSvgProps) {
  const resolvedGeometry = useMemo(
    () => geometry ?? createPipelineGeometry({
      orientation,
      readerWorkers: normalizedWorkerCount(snapshot.reader.workers),
      senderWorkers: normalizedWorkerCount(snapshot.sender.workers),
    }),
    [geometry, orientation, snapshot.reader.workers, snapshot.sender.workers],
  )
  const markers = usePipelineMarkerLifecycle(snapshot, resolvedGeometry)
  const readerChannelGeometry = resolvedGeometry.channels[snapshot.readerChannel.id]
  const senderChannelGeometry = resolvedGeometry.channels[snapshot.senderChannel.id]
  const throttlerTransform = resolvedGeometry.actors.throttler.transform

  return (
    <svg
      className={`pipeline-svg pipeline-svg--${orientation}`}
      viewBox={resolvedGeometry.viewBox.value}
      preserveAspectRatio="xMinYMin meet"
      role="group"
      aria-label="Reader to target load generation pipeline"
      data-layout={orientation}
    >
      <HttpLink
        snapshot={snapshot.http}
        selected={selectedId === snapshot.http.id}
        onSelect={onSelect}
        geometry={resolvedGeometry}
      />
      <ChannelCable
        snapshot={snapshot.readerChannel}
        geometry={readerChannelGeometry}
        selected={selectedId === snapshot.readerChannel.id}
        onSelect={onSelect}
        onCapacityChange={onChannelCapacityChange}
        orientation={orientation}
        capacityValues={snapshot.adapterKind === 'http'
          ? snapshot.policy?.readerChannelCapacity.allowed
          : undefined}
      />
      <ChannelCable
        snapshot={snapshot.senderChannel}
        geometry={senderChannelGeometry}
        selected={selectedId === snapshot.senderChannel.id}
        onSelect={onSelect}
        onCapacityChange={onChannelCapacityChange}
        orientation={orientation}
      />
      <ellipse
        cx={VALVE_APERTURE.centerX}
        cy={VALVE_APERTURE.centerY}
        rx={VALVE_APERTURE.radiusX}
        ry={VALVE_APERTURE.radiusY}
        fill="#03111f"
        className="pipeline-valve-vacuum"
        data-vacuum-fill="uniform"
        transform={throttlerTransform.x === 0 && throttlerTransform.y === 0
          ? undefined
          : `translate(${throttlerTransform.x} ${throttlerTransform.y})`}
      />
      <PipelineMarkers
        snapshot={snapshot}
        markers={markers}
        geometry={resolvedGeometry}
      />
      <ReaderActor
        snapshot={snapshot.reader}
        rateDriven={snapshot.adapterKind === 'http'}
        selected={selectedId === snapshot.reader.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
        geometry={resolvedGeometry.actors.reader}
        orientation={resolvedGeometry.orientation}
      />
      <ThrottlerActor
        snapshot={snapshot.throttler}
        upstreamChannel={snapshot.readerChannel}
        previewTps={requestedTpsPreview}
        selected={selectedId === snapshot.throttler.id}
        onSelect={onSelect}
        onPreviewTpsChange={onRequestedTpsPreviewChange}
        onRequestedTpsChange={onRequestedTpsChange}
        onInstallationModeChange={onInstallationModeChange}
        geometry={resolvedGeometry.actors.throttler}
        orientation={resolvedGeometry.orientation}
      />
      <SenderActor
        snapshot={snapshot.sender}
        selected={selectedId === snapshot.sender.id}
        onSelect={onSelect}
        onWorkerCountChange={onWorkerCountChange}
        geometry={resolvedGeometry.actors.sender}
        orientation={resolvedGeometry.orientation}
      />
      <TargetActor
        snapshot={snapshot.target}
        selected={selectedId === snapshot.target.id}
        onSelect={onSelect}
        geometry={resolvedGeometry.actors.target}
      />
    </svg>
  )
}
