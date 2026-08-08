import type { KeyboardEvent } from 'react'
import type { HttpSnapshot, SelectableId } from '../../model/loadgen'
import { formatInteger, formatMilliseconds, formatRate } from './formatters'
import { ACTOR_GEOMETRY } from './geometry'

interface HttpLinkProps {
  snapshot: HttpSnapshot
  selected: boolean
  onSelect: (id: SelectableId) => void
}

function linkStateClass(snapshot: HttpSnapshot): string {
  if (
    snapshot.connectionState === 'error' ||
    (snapshot.statusCode !== null && snapshot.statusCode >= 500)
  ) {
    return 'pipeline-http--error'
  }
  if (snapshot.connectionState === 'disconnected') {
    return 'pipeline-http--stopped'
  }
  if (
    snapshot.connectionState === 'connecting' ||
    (snapshot.statusCode !== null && snapshot.statusCode >= 400)
  ) {
    return 'pipeline-http--warning'
  }
  return 'pipeline-http--normal'
}

export function HttpLink({ snapshot, selected, onSelect }: HttpLinkProps) {
  const start = ACTOR_GEOMETRY.sender.ports.output
  const end = ACTOR_GEOMETRY.target.ports.input
  const centerX = (start.x + end.x) / 2
  const status = snapshot.statusCode === null ? '—' : snapshot.statusCode
  const inFlight = formatInteger(snapshot.inFlightRequests)
  const handleKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    onSelect(snapshot.id)
  }

  return (
    <g
      id="http-link"
      className={`pipeline-http pipeline-selectable ${linkStateClass(snapshot)}${selected ? ' pipeline-selectable--selected' : ''}`}
      role="button"
      tabIndex={0}
      aria-label={`Inspect HTTP connection, ${snapshot.connectionState}`}
      aria-pressed={selected}
      onClick={() => onSelect(snapshot.id)}
      onKeyDown={handleKeyDown}
    >
      <path
        d={`M${start.x} ${start.y} H${end.x}`}
        className="pipeline-link-hit-area"
        aria-hidden="true"
      />
      <path
        d={`M${start.x} ${start.y} H${end.x}`}
        className="pipeline-http-line"
      />
      <path
        d={`M${end.x} ${end.y} l-12 -7 v14 z`}
        className="pipeline-http-arrow"
      />
      <text
        x={centerX}
        y="444"
        textAnchor="middle"
        className="pipeline-small-strong pipeline-http-status"
      >
        HTTP {status}
      </text>
      <text x={centerX} y="463" textAnchor="middle" className="pipeline-small">
        {formatRate(snapshot.throughputTps)}
      </text>
      <text x={centerX} y="482" textAnchor="middle" className="pipeline-small">
        {inFlight === '—' ? '—' : `${inFlight} in-flight`} · p95{' '}
        {formatMilliseconds(snapshot.latencyP95Ms)}
      </text>
    </g>
  )
}
