import type { KeyboardEvent } from 'react'
import type { HttpSnapshot, SelectableId } from '../../model/loadgen'
import { formatInteger, formatMilliseconds, formatRate } from './formatters'
import type { PipelineGeometry } from './geometry'

interface HttpLinkProps {
  snapshot: HttpSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
  geometry: PipelineGeometry
}

function linkStateClass(
  snapshot: HttpSnapshot,
  presentedStatusCode: number | null,
): string {
  if (snapshot.connectionState === 'error') return 'pipeline-http--error'
  if (snapshot.connectionState === 'disconnected') {
    return 'pipeline-http--stopped'
  }
  if (snapshot.connectionState === 'connecting') return 'pipeline-http--warning'
  if (presentedStatusCode !== null && presentedStatusCode >= 500) {
    return 'pipeline-http--error'
  }
  if (presentedStatusCode !== null && presentedStatusCode >= 400) {
    return 'pipeline-http--warning'
  }
  return 'pipeline-http--normal'
}

export function HttpLink({
  snapshot,
  selected,
  onSelect,
  geometry,
}: HttpLinkProps) {
  const { start, end, metrics } = geometry.http
  const portrait = geometry.orientation === 'portrait'
  const path = portrait
    ? `M${start.x} ${start.y} V${end.y}`
    : `M${start.x} ${start.y} H${end.x}`
  const isIdle =
    snapshot.throughputTps === 0 && snapshot.inFlightRequests === 0
  const presentedStatusCode = isIdle ? null : snapshot.statusCode
  const status = presentedStatusCode === null ? '—' : presentedStatusCode
  const inFlight = formatInteger(snapshot.inFlightRequests)
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  return (
    <g
      id="http-link"
      className={`pipeline-http pipeline-selectable ${linkStateClass(snapshot, presentedStatusCode)}${selected ? ' pipeline-selectable--selected' : ''}`}
      role="button"
      tabIndex={0}
      aria-label={`Inspect HTTP connection, ${snapshot.connectionState}`}
      aria-pressed={selected}
      onClick={() => onSelect(snapshot.id)}
      onKeyDown={handleKeyDown}
    >
      <path
        d={path}
        className="pipeline-link-hit-area"
        aria-hidden="true"
      />
      <path
        d={path}
        className="pipeline-http-line"
      />
      <path
        d={portrait
          ? `M${end.x} ${end.y} l-7 -12 h14 z`
          : `M${end.x} ${end.y} l-12 -7 v14 z`}
        className="pipeline-http-arrow"
      />
      <text
        x={metrics.x}
        y={metrics.statusY}
        textAnchor="middle"
        className="pipeline-small-strong pipeline-http-status"
      >
        HTTP {status}
      </text>
      <text x={metrics.x} y={metrics.throughputY} textAnchor="middle" className="pipeline-small pipeline-http-throughput">
        {formatRate(snapshot.throughputTps)}
      </text>
      <text x={metrics.x} y={metrics.detailY} textAnchor="middle" className="pipeline-small pipeline-http-detail">
        {inFlight === '—' ? '—' : `${inFlight} in-flight`} · p95{' '}
        {formatMilliseconds(snapshot.latencyP95Ms)}
      </text>
    </g>
  )
}
