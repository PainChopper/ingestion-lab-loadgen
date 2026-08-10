import { useEffect, useState } from 'react'

export type PipelineLayoutMode = 'auto' | 'portrait' | 'landscape'
export type PipelineOrientation = Exclude<PipelineLayoutMode, 'auto'>

export function getPipelineLayoutMode(search: string): PipelineLayoutMode {
  const value = new URLSearchParams(search).get('layout')
  return value === 'portrait' || value === 'landscape' ? value : 'auto'
}

export function resolvePipelineOrientation(
  mode: PipelineLayoutMode,
  portraitMediaMatches: boolean,
  viewportWidth: number,
  viewportHeight: number,
): PipelineOrientation {
  if (mode !== 'auto') return mode
  if (viewportWidth === viewportHeight) return 'landscape'
  return portraitMediaMatches ? 'portrait' : 'landscape'
}

function currentOrientation(
  mode: PipelineLayoutMode,
  media: MediaQueryList,
): PipelineOrientation {
  return resolvePipelineOrientation(
    mode,
    media.matches,
    window.innerWidth,
    window.innerHeight,
  )
}

export function usePipelineOrientation(): PipelineOrientation {
  const mode = getPipelineLayoutMode(window.location.search)
  const [orientation, setOrientation] = useState<PipelineOrientation>(() => {
    const media = window.matchMedia('(orientation: portrait)')
    return currentOrientation(mode, media)
  })

  useEffect(() => {
    const media = window.matchMedia('(orientation: portrait)')
    const update = () => setOrientation(currentOrientation(mode, media))
    update()
    media.addEventListener('change', update)
    window.addEventListener('resize', update)
    return () => {
      media.removeEventListener('change', update)
      window.removeEventListener('resize', update)
    }
  }, [mode])

  return orientation
}
