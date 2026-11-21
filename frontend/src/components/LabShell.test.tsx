import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { StrictMode } from 'react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import { HttpAdapter } from '../adapters/HttpAdapter'
import { readWireFixture } from '../test/wireFixture'
import { LabShell } from './LabShell'

let adapter: LoadgenAdapter | null = null

afterEach(() => {
  adapter?.dispose()
  adapter = null
  window.history.replaceState(null, '', '/')
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('LabShell', () => {
  it('keeps HTTP cold start unknown with stopped channels and unavailable controls', () => {
    vi.useFakeTimers()
    const fetch = vi.fn(() => new Promise<Response>(() => undefined))
    vi.stubGlobal('fetch', fetch)
    adapter = new HttpAdapter()
    const view = render(<LabShell adapter={adapter} />)

    expect(adapter.getSnapshot().runState).toBeNull()
    expect(view.container.querySelector('#run-state')?.textContent).toBe('connecting')
    expect(view.container.querySelectorAll('.pipeline-channel')).toHaveLength(2)
    for (const channel of view.container.querySelectorAll('.pipeline-channel')) {
      expect(channel.classList.contains('pipeline-channel--stopped')).toBe(true)
    }
    expect(
      view.container.querySelector('#sender-actor .pipeline-worker-primary')?.textContent,
    ).toBe('No data')
    for (const name of ['Add reader worker', 'Add sender worker']) {
      expect(screen.getByRole<HTMLButtonElement>('button', { name }).disabled).toBe(true)
    }
    expect(screen.queryByRole('slider', { name: 'Throttle opening' })).toBeNull()
    expect(view.container.querySelector('.pipeline-valve-readonly-label')?.textContent).toBe(
      'UNAVAILABLE',
    )
    fireEvent.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', { name: 'Throttler valve installed' })
        .disabled,
    ).toBe(true)
    expect(
      screen.getByRole<HTMLInputElement>('spinbutton', { name: /^Requested TPS/ }).disabled,
    ).toBe(true)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('recovers after the first HTTP failure and retains measurements after connection loss', async () => {
    vi.useFakeTimers()
    const wire = readWireFixture()
    wire.run.state = 'running'
    wire.run.elapsedMs = 1_000
    wire.reader.workers = 1
    wire.sender.workers = 2
    wire.reader.sourceError = null
    wire.throttler.installed = true
    const fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('offline'))
      .mockResolvedValueOnce(
        new Response(JSON.stringify(wire), { headers: { 'Content-Type': 'application/json' } }),
      )
      .mockRejectedValue(new TypeError('offline'))
    vi.stubGlobal('fetch', fetch)
    adapter = new HttpAdapter()
    const view = render(<LabShell adapter={adapter} />)
    await act(async () => {
      await Promise.resolve()
    })
    expect(adapter.getSnapshot()).toMatchObject({ runState: null, connectionState: 'error' })
    expect(view.container.querySelector('#run-state')?.textContent).toBe('error')
    expect(screen.queryByText('idle')).toBeNull()
    expect(view.container.querySelectorAll('.pipeline-channel')).toHaveLength(2)
    for (const channel of view.container.querySelectorAll('.pipeline-channel')) {
      expect(channel.classList.contains('pipeline-channel--stopped')).toBe(true)
    }

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
    expect(adapter.getSnapshot()).toMatchObject({
      runState: 'running',
      connectionState: 'connected',
    })
    expect(view.container.querySelector('#run-state')?.textContent).toBe('running')
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Add reader worker' }).disabled,
    ).toBe(false)
    expect(
      view.container.querySelector('#sender-actor .pipeline-worker-primary')?.textContent,
    ).toBe('No data')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
    expect(adapter.getSnapshot()).toMatchObject({
      runState: 'running',
      connectionState: 'error',
      elapsedMs: 1_000,
      totalTransactions: 101,
    })
    expect(view.container.querySelector('#run-state')?.textContent).toBe('running')
    expect(
      view.container.querySelector('#sender-actor .pipeline-worker-primary')?.textContent,
    ).toBe('No data')
    for (const channel of view.container.querySelectorAll('.pipeline-channel')) {
      expect(channel.classList.contains('pipeline-channel--connection-error')).toBe(true)
    }
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Add reader worker' }).disabled,
    ).toBe(true)
    expect(
      screen.getByRole('slider', { name: 'Throttle opening' }).getAttribute('aria-disabled'),
    ).toBe('true')
  })

  it('autostarts in StrictMode and advances the counters without input', () => {
    vi.useFakeTimers()
    adapter = new SimulationAdapter()
    const view = render(
      <StrictMode>
        <LabShell adapter={adapter} />
      </StrictMode>,
    )

    screen.getByText('running')
    screen.getByText('00:00:00')
    expect(vi.getTimerCount()).toBe(1)

    act(() => vi.advanceTimersByTime(1_000))
    expect(adapter.getSnapshot().elapsedMs).toBe(1_000)
    expect(adapter.getSnapshot().totalTransactions).toBeGreaterThan(0)
    screen.getByText('00:00:01')
    view.unmount()
    expect(vi.getTimerCount()).toBe(1)
  })

  it('renders a Reader source error overlay and clears it on the next snapshot', async () => {
    vi.useFakeTimers()
    const wire = readWireFixture()
    const response = (body: unknown) =>
      new Response(JSON.stringify(body), {
        headers: { 'Content-Type': 'application/json' },
      })
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(response(wire))
        .mockResolvedValue(response({ ...wire, reader: { ...wire.reader, sourceError: null } })),
    )
    adapter = new HttpAdapter()
    render(<LabShell adapter={adapter} />)
    await act(async () => {
      await Promise.resolve()
    })
    expect(
      screen.getByRole('alert', { name: 'Reader source error: reader-operation' }).textContent,
    ).toBe('SOURCE ERROR')
    fireEvent.click(screen.getByRole('button', { name: 'Inspect reader' }))
    expect(screen.getByTestId('reader-source-error-label').parentElement?.textContent).toContain(
      'reader-operation: reader-source/reader-path: reader-message',
    )
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByTestId('reader-source-error-label')).toBeNull()
  })

  it('opens a real actor inspector and clears the selection', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    screen.getByText('No pipeline object selected')
    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))

    screen.getByRole('heading', { name: 'READER' })
    screen.getByLabelText('Reader configuration')
    expect(
      screen.getByRole('button', { name: 'Inspect reader' }).getAttribute('aria-pressed'),
    ).toBe('true')

    await user.click(screen.getByRole('button', { name: 'Clear selection' }))
    screen.getByText('No pipeline object selected')
    expect(
      screen.getByRole('button', { name: 'Inspect reader' }).getAttribute('aria-pressed'),
    ).toBe('false')
  })

  it('connects canvas worker controls and the Inspector mode toggle to the adapter', async () => {
    const user = userEvent.setup()
    const simulation = new SimulationAdapter()
    adapter = simulation
    const dispatch = vi.spyOn(simulation, 'dispatch')
    render(<LabShell adapter={simulation} />)
    await user.click(screen.getByRole('button', { name: 'Add reader worker' }))
    await user.click(screen.getByRole('button', { name: 'Add sender worker' }))
    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    const installed = screen.getByRole<HTMLInputElement>('checkbox', {
      name: 'Throttler valve installed',
    })
    await user.click(installed)
    expect(installed.checked).toBe(false)
    expect(dispatch.mock.calls.map(([command]) => command)).toEqual([
      { type: 'set-reader-workers', value: 6 },
      { type: 'set-sender-workers', value: 4 },
      { type: 'set-throttler-installed', value: false },
    ])
  })

  it('shows a named drawer only when selected and keeps keyboard focus in the diagram', async () => {
    vi.stubGlobal('innerWidth', 1120)
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)
    const reader = screen.getByRole('button', { name: 'Inspect reader' })

    expect(screen.queryByRole('complementary')).toBeNull()
    reader.focus()
    await user.keyboard('{Enter}')
    const inspector = screen.getByRole('complementary', { name: 'READER' })
    expect(document.activeElement).toBe(reader)
    await user.tab()
    expect(inspector.contains(document.activeElement)).toBe(false)
    await user.click(within(inspector).getByRole('button', { name: 'Clear selection' }))
    expect(screen.queryByRole('complementary')).toBeNull()
    expect(document.activeElement).toBe(reader)

    await user.click(reader)
    screen.getByRole('complementary', { name: 'READER' })
    await user.click(reader)
    expect(screen.queryByRole('complementary')).toBeNull()
  })

  it.each([1119, 1440])(
    'keeps selection on repeated activation outside the drawer at %i px',
    async (width) => {
      vi.stubGlobal('innerWidth', width)
      const user = userEvent.setup()
      adapter = new SimulationAdapter()
      render(<LabShell adapter={adapter} />)
      const sender = screen.getByRole('button', { name: /^Inspect sender,/ })

      screen.getByRole('complementary', { name: 'INSPECTOR' })
      await user.click(sender)
      await user.click(sender)
      screen.getByRole('complementary', { name: 'SENDER' })
    },
  )

  it('lets a number draft and valve gestures consume the first Escape in the drawer', async () => {
    vi.stubGlobal('innerWidth', 1439)
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    const view = render(<LabShell adapter={adapter} />)
    const throttler = screen.getByRole('button', { name: 'Inspect throttler' })
    await user.click(throttler)
    const inspector = screen.getByRole('complementary', { name: 'THROTTLER' })
    const requested = within(inspector).getByRole('spinbutton', { name: /^Requested TPS/ })

    await user.click(requested)
    fireEvent.change(requested, { target: { value: '5000' } })
    await user.keyboard('{Escape}')
    expect(screen.getByRole('complementary', { name: 'THROTTLER' })).toBe(inspector)
    expect((requested as HTMLInputElement).value).toBe('120000')
    expect(dispatch).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('complementary')).toBeNull())
    expect(document.activeElement).toBe(throttler)

    await user.click(throttler)
    const opening = view.container.querySelector('[data-wheel-direction="increase"]')!
    fireEvent.pointerDown(opening, { pointerId: 1 })
    expect(screen.getByRole('slider').getAttribute('aria-valuetext')).toContain('preview')
    await user.keyboard('{Escape}')
    screen.getByRole('complementary', { name: 'THROTTLER' })
    expect(screen.getByRole('slider').getAttribute('aria-valuetext')).toContain('applied')

    const installation = screen.getByRole('button', { name: 'Remove throttler valve' })
    fireEvent.pointerDown(installation, { clientX: 100, clientY: 100, pointerId: 2 })
    fireEvent.pointerMove(installation, { clientX: 100, clientY: 75, pointerId: 2 })
    expect(installation.getAttribute('data-candidate-kind')).toBe('preview')
    await user.keyboard('{Escape}')
    expect(installation.getAttribute('data-candidate-kind')).toBe('')
    screen.getByRole('complementary', { name: 'THROTTLER' })
    expect(dispatch).not.toHaveBeenCalled()
    installation.focus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('complementary')).toBeNull())
    expect(document.activeElement).toBe(installation)
  })

  it('renders only supported channel states in the legend', () => {
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.queryByText('Connection error')).toBeNull()
    for (const label of ['Normal flow', 'Near limit', 'Backpressure', 'Stopped']) {
      screen.getByText(label)
    }
    const legend = screen.getByRole('contentinfo', { name: 'Channel states' })
    expect(legend.querySelectorAll('i[aria-hidden="true"]')).toHaveLength(4)
  })

  it('preserves adapter state, selection, and valve focus on live resize', async () => {
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
    vi.stubGlobal('innerWidth', 1200)
    vi.stubGlobal('innerHeight', 700)
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    const valve = screen.getByRole('slider', { name: 'Throttle opening' })
    valve.focus()
    await user.keyboard('{End}')
    expect(screen.getByTestId('pipeline-viewport').dataset.layout).toBe('landscape')
    const callback = resizeCallbacks[0]
    if (callback === undefined) throw new Error('ResizeObserver was not attached')
    act(() =>
      callback([{ contentRect: { width: 1440 } } as ResizeObserverEntry], {} as ResizeObserver),
    )
    expect(screen.getByTestId('pipeline-viewport').dataset.contentWidth).toBe('1440')
    expect(document.querySelector('.pipeline-svg')?.getAttribute('viewBox')).toBe('0 22 1440 582')
    expect(document.activeElement).toBe(valve)

    vi.stubGlobal('innerWidth', 600)
    vi.stubGlobal('innerHeight', 900)
    act(() => window.dispatchEvent(new Event('resize')))

    expect(screen.getByTestId('pipeline-viewport').dataset.layout).toBe('portrait')
    expect(
      screen
        .getByRole('main', { name: 'Pipeline viewport' })
        .closest('.lab-shell')
        ?.getAttribute('data-layout'),
    ).toBe('portrait')
    screen.getByRole('heading', { name: 'THROTTLER' })
    expect(
      screen.getByRole('button', { name: 'Inspect throttler' }).getAttribute('aria-pressed'),
    ).toBe('true')
    screen.getByText('running')
    expect(adapter.getSnapshot().throttler.requestedTps.applied).toBe(250_000)
    expect(document.activeElement).toBe(valve)
    expect(document.querySelector('.pipeline-svg')?.getAttribute('viewBox')).toMatch(
      /^0 0 480 \d+$/,
    )
    expect(
      Number(document.querySelector('.pipeline-svg')?.getAttribute('viewBox')?.split(' ')[3]),
    ).toBeGreaterThan(1200)
  })
})
