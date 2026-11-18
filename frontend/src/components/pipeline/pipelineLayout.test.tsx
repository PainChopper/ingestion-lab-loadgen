import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getPipelineLayoutMode,
  resolvePipelineOrientation,
  usePipelineLayout,
} from './pipelineLayout'

function Probe() {
  const { orientation, inspectorLayout } = usePipelineLayout()
  return (
    <output aria-label="layout">
      {orientation} {inspectorLayout}
    </output>
  )
}

afterEach(() => {
  window.history.replaceState(null, '', '/')
  vi.unstubAllGlobals()
})

describe('pipeline layout selection', () => {
  it.each([
    ['', 'auto'],
    ['?layout=portrait', 'portrait'],
    ['?layout=landscape', 'landscape'],
    ['?layout=sideways', 'auto'],
  ] as const)('parses %s as %s', (search, expected) => {
    expect(getPipelineLayoutMode(search)).toBe(expected)
  })

  it('gives explicit query mode precedence and uses portrait at narrow widths', () => {
    expect(resolvePipelineOrientation('portrait', 1200)).toBe('portrait')
    expect(resolvePipelineOrientation('landscape', 600)).toBe('landscape')
    expect(resolvePipelineOrientation('auto', 615)).toBe('portrait')
    expect(resolvePipelineOrientation('auto', 1440)).toBe('landscape')
    expect(resolvePipelineOrientation('auto', 800)).toBe('portrait')
    expect(resolvePipelineOrientation('auto', 1119)).toBe('portrait')
    expect(resolvePipelineOrientation('auto', 1120)).toBe('landscape')
    expect(resolvePipelineOrientation('auto', 1280)).toBe('landscape')
  })

  it('follows width boundaries on initial render and resize without using physical orientation', () => {
    vi.stubGlobal('innerWidth', 1119)
    vi.stubGlobal('innerHeight', 2160)
    render(<Probe />)
    expect(screen.getByLabelText('layout').textContent).toBe('portrait stacked')

    for (const [width, layout] of [
      [1120, 'landscape drawer'],
      [1439, 'landscape drawer'],
      [1440, 'landscape column'],
      [1920, 'landscape column'],
      [1119, 'portrait stacked'],
    ] as const) {
      vi.stubGlobal('innerWidth', width)
      act(() => window.dispatchEvent(new Event('resize')))
      expect(screen.getByLabelText('layout').textContent).toBe(layout)
    }
  })
})
