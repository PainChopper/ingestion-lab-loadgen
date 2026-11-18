import { useEffect, useState } from 'react'

export type PipelineLayoutMode = 'auto' | 'portrait' | 'landscape'
export type PipelineOrientation = Exclude<PipelineLayoutMode, 'auto'>
export type PipelineInspectorLayout = 'stacked' | 'drawer' | 'column'

export const PIPELINE_LANDSCAPE_MIN_WIDTH = 1120
const INSPECTOR_COLUMN_MIN_WIDTH = 1440

export function getPipelineLayoutMode(search: string): PipelineLayoutMode {
  const value = new URLSearchParams(search).get('layout')
  return value === 'portrait' || value === 'landscape' ? value : 'auto'
}

export function resolvePipelineOrientation(
  mode: PipelineLayoutMode,
  viewportWidth: number,
): PipelineOrientation {
  if (mode !== 'auto') return mode
  return viewportWidth < PIPELINE_LANDSCAPE_MIN_WIDTH ? 'portrait' : 'landscape'
}

export function usePipelineLayout() {
  const mode = getPipelineLayoutMode(window.location.search)
  const [viewportWidth, setViewportWidth] = useState(() => window.innerWidth)

  useEffect(() => {
    const update = () => setViewportWidth(window.innerWidth)
    window.addEventListener('resize', update)
    update()
    return () => window.removeEventListener('resize', update)
  }, [])

  const orientation = resolvePipelineOrientation(mode, viewportWidth)
  const inspectorLayout: PipelineInspectorLayout =
    orientation === 'portrait'
      ? 'stacked'
      : viewportWidth < INSPECTOR_COLUMN_MIN_WIDTH
        ? 'drawer'
        : 'column'
  return {
    orientation,
    inspectorLayout,
    toggleSelection: inspectorLayout === 'drawer' && viewportWidth >= PIPELINE_LANDSCAPE_MIN_WIDTH,
  }
}
