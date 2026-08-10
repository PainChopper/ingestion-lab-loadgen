import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import { LabShell } from './LabShell'

let adapter: SimulationAdapter | null = null

afterEach(() => {
  adapter?.dispose()
  adapter = null
  window.history.replaceState(null, '', '/')
  vi.unstubAllGlobals()
})

describe('LabShell', () => {
  it('runs, pauses, and resets through the mounted toolbar', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Start run' }))
    await waitFor(() => {
      expect(screen.getByText('running')).not.toBeNull()
    })

    await user.click(screen.getByRole('button', { name: 'Pause run' }))
    await waitFor(() => {
      expect(screen.getByText('paused')).not.toBeNull()
    })

    await user.click(screen.getByRole('button', { name: 'Reset run' }))
    await waitFor(() => {
      expect(screen.getByText('idle')).not.toBeNull()
      expect(screen.getByText('00:00:00')).not.toBeNull()
    })
  })

  it('opens a real actor inspector and clears the selection', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))

    expect(screen.getByRole('heading', { name: 'READER' })).not.toBeNull()
    expect(screen.getByLabelText('Reader configuration')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect reader' })
      .getAttribute('aria-pressed')).toBe('true')

    await user.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect reader' })
      .getAttribute('aria-pressed')).toBe('false')
  })

  it('shares numeric TPS preview and absolute command with the valve', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)
    const input = screen.getByRole('spinbutton', { name: /^Requested TPS/ })
    const valve = screen.getByRole('slider', { name: 'Throttle opening' })

    expect(valve.getAttribute('aria-valuenow')).toBe('5')
    await user.clear(input)
    await user.type(input, '135000')
    expect(valve.getAttribute('aria-valuenow')).toBe('6')
    expect(dispatch).not.toHaveBeenCalled()

    await user.keyboard('{Escape}')
    expect(valve.getAttribute('aria-valuenow')).toBe('5')
    await user.click(input)
    await user.clear(input)
    await user.type(input, '135000')
    await user.keyboard('{Enter}')

    await waitFor(() => {
      expect(dispatch).toHaveBeenCalledWith({
        type: 'set-requested-tps',
        value: 135_000,
      })
      expect(valve.getAttribute('aria-valuenow')).toBe('6')
      expect(screen.getByText('55% OPEN')).not.toBeNull()
    })
  })

  it('preserves adapter state, selection, inspector, and preview on live resize', async () => {
    const user = userEvent.setup()
    const resizeCallbacks: ResizeObserverCallback[] = []
    class MockResizeObserver implements ResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallbacks.push(callback)
      }

      observe() {}
      unobserve() {}
      disconnect() {}
    }
    vi.stubGlobal('ResizeObserver', MockResizeObserver)
    let portrait = false
    const orientationListeners = new Set<() => void>()
    vi.mocked(window.matchMedia).mockImplementation((query: string) => {
      const media = {
        media: query,
        onchange: null,
        addEventListener: vi.fn((_name: string, listener: () => void) => {
          if (query === '(orientation: portrait)') {
            orientationListeners.add(listener)
          }
        }),
        removeEventListener: vi.fn((_name: string, listener: () => void) => {
          orientationListeners.delete(listener)
        }),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }
      return Object.defineProperty(media, 'matches', {
        get: () => query === '(orientation: portrait)' && portrait,
      }) as unknown as MediaQueryList
    })
    Object.defineProperties(window, {
      innerWidth: { configurable: true, value: 1200 },
      innerHeight: { configurable: true, value: 700 },
    })
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))
    await user.click(screen.getByRole('button', { name: 'Start run' }))
    const requested = screen.getByRole('spinbutton', { name: /^Requested TPS/ })
    await user.clear(requested)
    await user.type(requested, '135000')
    expect(screen.getByTestId('pipeline-viewport').dataset.layout)
      .toBe('landscape')
    const callback = resizeCallbacks[0]
    if (callback === undefined) throw new Error('ResizeObserver was not attached')
    act(() => callback([
      { contentRect: { width: 1440 } } as ResizeObserverEntry,
    ], {} as ResizeObserver))
    expect(screen.getByTestId('pipeline-viewport').dataset.contentWidth)
      .toBe('1440')
    expect(document.querySelector('.pipeline-svg')?.getAttribute('viewBox'))
      .toBe('0 0 1440 650')
    expect(document.activeElement).toBe(requested)

    portrait = true
    Object.defineProperties(window, {
      innerWidth: { configurable: true, value: 600 },
      innerHeight: { configurable: true, value: 900 },
    })
    act(() => orientationListeners.forEach((listener) => listener()))

    expect(screen.getByTestId('pipeline-viewport').dataset.layout)
      .toBe('portrait')
    expect(screen.getByRole('heading', { name: 'READER' })).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect reader' })
      .getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByText('running')).not.toBeNull()
    expect((requested as HTMLInputElement).value).toBe('135000')
    expect(document.activeElement).toBe(requested)
    expect(document.querySelector('.pipeline-svg')?.getAttribute('viewBox'))
      .toMatch(/^0 0 480 1[34]\d{2}$/)
  })
})
