import type { PipelineGeometry } from './geometry'

interface HttpLinkProps {
  geometry: PipelineGeometry
}

export function HttpLink({ geometry }: HttpLinkProps) {
  const { start, end } = geometry.http
  const portrait = geometry.orientation === 'portrait'
  const path = portrait
    ? `M${start.x} ${start.y} V${start.y + 16} Q${start.x} ${start.y + 24} ${start.x - 8} ${start.y + 24} H88 Q80 ${start.y + 24} 80 ${start.y + 32} V${end.y - 8} Q80 ${end.y} 88 ${end.y} H${end.x}`
    : `M${start.x} ${start.y} H${end.x}`

  return (
    <g id="http-link" className="pipeline-http" aria-hidden="true">
      <path d={path} className="pipeline-http-line" />
      <path d={`M${end.x} ${end.y} l-12 -7 v14 z`} className="pipeline-http-arrow" />
    </g>
  )
}
