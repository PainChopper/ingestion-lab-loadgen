import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getPipelineLayoutMode,
  resolvePipelineOrientation,
  usePipelineOrientation,
} from './pipelineLayout'

function Probe() {
  return <output aria-label="layout">{usePipelineOrientation()}</output>
}

afterEach(() => {
  window.history.replaceState(null, '', '/')
  Object.defineProperties(window, {
    innerWidth: { configurable: true, value: 1024 },
    innerHeight: { configurable: true, value: 768 },
  })
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

  it('gives explicit query mode precedence and treats square as landscape', () => {
    expect(resolvePipelineOrientation('portrait', false, 1200, 600))
      .toBe('portrait')
    expect(resolvePipelineOrientation('landscape', true, 600, 1200))
      .toBe('landscape')
    expect(resolvePipelineOrientation('auto', true, 800, 800))
      .toBe('landscape')
  })

  it('follows live orientation media changes in auto mode', () => {
    let portrait = false
    const listeners = new Set<() => void>()
    const media = {
      get matches() { return portrait },
      media: '(orientation: portrait)',
      onchange: null,
      addEventListener: vi.fn((_name: string, listener: () => void) => {
        listeners.add(listener)
      }),
      removeEventListener: vi.fn((_name: string, listener: () => void) => {
        listeners.delete(listener)
      }),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }
    vi.mocked(window.matchMedia).mockImplementation(() => media)
    Object.defineProperties(window, {
      innerWidth: { configurable: true, value: 1200 },
      innerHeight: { configurable: true, value: 700 },
    })

    render(<Probe />)
    expect(screen.getByLabelText('layout').textContent).toBe('landscape')

    portrait = true
    Object.defineProperties(window, {
      innerWidth: { configurable: true, value: 600 },
      innerHeight: { configurable: true, value: 900 },
    })
    act(() => listeners.forEach((listener) => listener()))
    expect(screen.getByLabelText('layout').textContent).toBe('portrait')

    Object.defineProperties(window, {
      innerWidth: { configurable: true, value: 800 },
      innerHeight: { configurable: true, value: 800 },
    })
    act(() => window.dispatchEvent(new Event('resize')))
    expect(screen.getByLabelText('layout').textContent).toBe('landscape')
  })
})
