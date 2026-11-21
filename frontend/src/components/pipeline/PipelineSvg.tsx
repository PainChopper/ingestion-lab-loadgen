import { formatInteger } from './formatters'
import type { LoadgenSnapshot, SelectableId } from '../../model/loadgen'
import type { LiveControls } from '../../hooks/useDesiredControl'
import { HttpLink } from './HttpLink'
import type { PipelineGeometry } from './geometry'
import { ChannelCable } from './ChannelCable'
import { ReaderActor } from './ReaderActor'
import { SenderActor } from './SenderActor'
import { TargetActor } from './TargetActor'
import { ThrottlerActor } from './ThrottlerActor'
import { VALVE_APERTURE } from './throttlerValve'
import './PipelineSvg.css'

interface PipelineSvgProps {
  snapshot: LoadgenSnapshot
  selectedId: SelectableId | null
  onSelect: (id: SelectableId) => void
  liveControls: LiveControls
  geometry: PipelineGeometry
}

export function PipelineSvg({
  snapshot,
  selectedId,
  onSelect,
  liveControls,
  geometry,
}: PipelineSvgProps) {
  const { orientation } = geometry
  const readerChannelGeometry = geometry.channels[snapshot.readerChannel.id]
  const senderChannelGeometry = geometry.channels[snapshot.senderChannel.id]
  const throttlerTransform = geometry.actors.throttler.transform

  return (
    <svg
      className={`pipeline-svg pipeline-svg--${orientation}`}
      viewBox={geometry.viewBox.value}
      preserveAspectRatio="xMinYMin meet"
      role="group"
      aria-label="Reader to target load generation pipeline"
      data-layout={orientation}
    >
      <HttpLink geometry={geometry} />
      <ChannelCable
        snapshot={snapshot.readerChannel}
        batchSize={snapshot.reader.readBatchSize}
        geometry={readerChannelGeometry}
        selected={selectedId === snapshot.readerChannel.id}
        onSelect={onSelect}
        orientation={orientation}
        hoseForbiddenBoxes={
          orientation === 'portrait'
            ? [
                { x: 70, y: readerChannelGeometry.end.y - 196, width: 340, height: 188 },
                geometry.batchLabel.guard,
              ]
            : undefined
        }
      />
      <ChannelCable
        snapshot={snapshot.senderChannel}
        batchSize={snapshot.reader.readBatchSize}
        geometry={senderChannelGeometry}
        selected={selectedId === snapshot.senderChannel.id}
        onSelect={onSelect}
        orientation={orientation}
        hoseForbiddenBoxes={
          orientation === 'portrait'
            ? [{ x: 70, y: senderChannelGeometry.end.y - 196, width: 340, height: 188 }]
            : undefined
        }
      />
      <ellipse
        cx={VALVE_APERTURE.centerX}
        cy={VALVE_APERTURE.centerY}
        rx={VALVE_APERTURE.radiusX}
        ry={VALVE_APERTURE.radiusY}
        className="pipeline-valve-vacuum"
        data-vacuum-fill="uniform"
        transform={
          throttlerTransform.x === 0 && throttlerTransform.y === 0
            ? undefined
            : `translate(${throttlerTransform.x} ${throttlerTransform.y})`
        }
      />
      <ReaderActor
        snapshot={snapshot.reader}
        runState={snapshot.runState}
        rateDriven={snapshot.adapterKind === 'http'}
        selected={selectedId === snapshot.reader.id}
        onSelect={onSelect}
        desiredControl={liveControls.readerWorkers}
        geometry={geometry.actors.reader}
        orientation={orientation}
      />
      <ThrottlerActor
        snapshot={snapshot.throttler}
        upstreamChannel={snapshot.readerChannel}
        requestedTpsControl={liveControls.requestedTps}
        installedControl={liveControls.installed}
        selected={selectedId === snapshot.throttler.id}
        onSelect={onSelect}
        geometry={geometry.actors.throttler}
        orientation={orientation}
      />
      <text
        className="pipeline-batch-size"
        x={geometry.batchLabel.anchor.x}
        y={geometry.batchLabel.anchor.y + 5}
        textAnchor="middle"
      >
        Batch {formatInteger(snapshot.reader.readBatchSize)} tx
      </text>
      <SenderActor
        snapshot={snapshot.sender}
        runState={snapshot.runState}
        outputTps={snapshot.senderChannel.outputTransactionsPerSecond}
        selected={selectedId === snapshot.sender.id}
        onSelect={onSelect}
        desiredControl={liveControls.senderWorkers}
        geometry={geometry.actors.sender}
        orientation={orientation}
      />
      <TargetActor geometry={geometry.actors.target} />
    </svg>
  )
}
