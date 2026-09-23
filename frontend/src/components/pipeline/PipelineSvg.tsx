import type {
  LoadgenSnapshot,
  ChannelId,
  SelectableId,
} from '../../model/loadgen'
import type { LiveControls } from '../../hooks/useDesiredControl'
import { useMemo } from 'react'
import { HttpLink } from './HttpLink'
import {
  createPipelineGeometry,
  type PipelineGeometry,
} from './geometry'
import { ChannelCable } from './ChannelCable'
import { PipelineBatchControl } from './PipelineBatchControl'
import { ReaderActor } from './ReaderActor'
import { SenderActor } from './SenderActor'
import { TargetActor } from './TargetActor'
import { ThrottlerActor } from './ThrottlerActor'
import { VALVE_APERTURE } from './throttlerValve'
import type { PipelineOrientation } from './pipelineLayout'
import './PipelineSvg.css'

interface PipelineSvgProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  onChannelCapacityChange: (channel: ChannelId, value: number) => void
  liveControls: LiveControls
  frozenObserved?: boolean
  orientation?: PipelineOrientation
  geometry?: PipelineGeometry
}

export function PipelineSvg({
  snapshot,
  selectedId,
  onSelect,
  onChannelCapacityChange,
  liveControls,
  frozenObserved = false,
  orientation = 'landscape',
  geometry,
}: PipelineSvgProps) {
  const resolvedGeometry = useMemo(
    () => geometry ?? createPipelineGeometry({
      orientation,
      readerWorkers: liveControls.readerWorkers.desired,
      senderWorkers: liveControls.senderWorkers.desired,
    }),
    [geometry, liveControls.readerWorkers.desired, liveControls.senderWorkers.desired, orientation],
  )
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
        telemetryAvailable={snapshot.adapterKind !== 'http'}
        selected={selectedId === snapshot.http.id}
        onSelect={onSelect}
        geometry={resolvedGeometry}
      />
      <ChannelCable
        snapshot={snapshot.readerChannel}
        batchSize={snapshot.reader.readBatchSize}
        geometry={readerChannelGeometry}
        selected={selectedId === snapshot.readerChannel.id}
        onSelect={onSelect}
        onCapacityChange={onChannelCapacityChange}
        orientation={orientation}
        capacityValues={snapshot.adapterKind === 'http'
          ? snapshot.policy?.readerChannelCapacity.allowed
          : undefined}
        hoseForbiddenBoxes={orientation === 'portrait'
          ? [resolvedGeometry.batchControl.guard]
          : undefined}
      />
      <ChannelCable
        snapshot={snapshot.senderChannel}
        batchSize={snapshot.reader.readBatchSize}
        geometry={senderChannelGeometry}
        selected={selectedId === snapshot.senderChannel.id}
        onSelect={onSelect}
        onCapacityChange={onChannelCapacityChange}
        orientation={orientation}
        capacityValues={snapshot.adapterKind === 'http'
          ? snapshot.policy?.senderChannelCapacity.allowed
          : undefined}
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
      <ReaderActor
        snapshot={snapshot.reader}
        rateDriven={snapshot.adapterKind === 'http'}
        selected={selectedId === snapshot.reader.id}
        onSelect={onSelect}
        desiredControl={liveControls.readerWorkers}
        frozenObserved={frozenObserved}
        geometry={resolvedGeometry.actors.reader}
        orientation={resolvedGeometry.orientation}
      />
      <ThrottlerActor
        snapshot={snapshot.throttler}
        upstreamChannel={snapshot.readerChannel}
        requestedTpsControl={liveControls.requestedTps}
        installationModeControl={liveControls.installationMode}
        selected={selectedId === snapshot.throttler.id}
        onSelect={onSelect}
        geometry={resolvedGeometry.actors.throttler}
        orientation={resolvedGeometry.orientation}
      />
      <PipelineBatchControl
        control={snapshot.reader.readBatchSize}
        desiredControl={liveControls.readBatchSize}
        anchor={resolvedGeometry.batchControl.anchor}
      />
      <SenderActor
        snapshot={snapshot.sender}
        selected={selectedId === snapshot.sender.id}
        onSelect={onSelect}
        desiredControl={liveControls.senderWorkers}
        frozenObserved={frozenObserved}
        geometry={resolvedGeometry.actors.sender}
        orientation={resolvedGeometry.orientation}
      />
      <TargetActor
        snapshot={snapshot.target}
        telemetryAvailable={snapshot.adapterKind !== 'http'}
        selected={selectedId === snapshot.target.id}
        onSelect={onSelect}
        geometry={resolvedGeometry.actors.target}
      />
    </svg>
  )
}
