import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { StrictMode } from 'react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LoadgenAdapter } from '../adapters/LoadgenAdapter'
import { SimulationAdapter } from '../adapters/SimulationAdapter'
import type { LoadgenTelemetrySnapshot } from '../model/loadgen'
import { LabShell } from './LabShell'

let adapter: SimulationAdapter | null = null

afterEach(() => {
  adapter?.dispose()
  adapter = null
  window.history.replaceState(null, '', '/')
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('LabShell', () => {
  it('autostarts in StrictMode and advances the counters without input', () => {
    vi.useFakeTimers()
    adapter = new SimulationAdapter()
    const view = render(
      <StrictMode>
        <LabShell adapter={adapter} />
      </StrictMode>,
    )

    expect(screen.getByText('running')).not.toBeNull()
    expect(screen.getByText('00:00:00')).not.toBeNull()
    expect(vi.getTimerCount()).toBe(1)

    act(() => vi.advanceTimersByTime(1_000))
    expect(adapter.getSnapshot().elapsedMs).toBe(1_000)
    expect(adapter.getSnapshot().totalTransactions).toBeGreaterThan(0)
    expect(screen.getByText('00:00:01')).not.toBeNull()
    view.unmount()
    expect(vi.getTimerCount()).toBe(1)
  })

  it('renders a Reader source error overlay and clears it on the next snapshot', async () => {
    const simulation = new SimulationAdapter()
    let snapshot: LoadgenTelemetrySnapshot = {
      ...simulation.getSnapshot(),
      runState: 'faulted',
      reader: {
        ...simulation.getSnapshot().reader,
        state: 'faulted',
        sourceDirectory: 'C:/dataset',
        sourceError: {
          category: 'source',
          operation: 'read',
          relativePath: 'broken.parquet',
          message: 'corrupt parquet',
        },
      },
    }
    const listeners = new Set<(next: LoadgenTelemetrySnapshot) => void>()
    const errorAdapter: LoadgenAdapter = {
      kind: 'simulation',
      getSnapshot: () => snapshot,
      subscribe: (listener) => {
        listeners.add(listener)
        listener(snapshot)
        return () => listeners.delete(listener)
      },
      dispatch: simulation.dispatch,
      dispose: simulation.dispose,
    }
    render(<LabShell adapter={errorAdapter} />)

    expect(screen.getByRole('alert').textContent).toBe('SOURCE ERROR')
    await userEvent.setup().click(screen.getByRole('button', { name: 'Inspect reader' }))
    const label = screen.getByTestId('reader-source-error-label')
    expect(label.textContent).toBe('SOURCE ERROR')
    expect(label.parentElement?.className).toBe(
      'inspector-data__row--full-width inspector-data__row--source-error',
    )
    expect(label.nextElementSibling?.tagName).toBe('DD')
    expect(label.parentElement?.querySelectorAll('dd')).toHaveLength(1)

    act(() => {
      snapshot = {
        ...snapshot,
        runState: 'idle',
        reader: { ...snapshot.reader, state: 'idle', sourceError: null },
      }
      listeners.forEach((listener) => listener(snapshot))
    })
    expect(screen.queryByRole('alert')).toBeNull()
    errorAdapter.dispose()
  })

  it('disables unavailable canvas worker controls without dispatching', async () => {
    const simulation = new SimulationAdapter()
    const initial = simulation.getSnapshot()
    const snapshot: LoadgenTelemetrySnapshot = {
      ...initial,
      reader: {
        ...initial.reader,
        workers: { ...initial.reader.workers, applied: null },
      },
      sender: {
        ...initial.sender,
        workers: { ...initial.sender.workers, applied: null },
      },
    }
    const dispatch = vi.fn()
    const unavailableAdapter: LoadgenAdapter = {
      kind: 'simulation',
      getSnapshot: () => snapshot,
      subscribe: (listener) => {
        listener(snapshot)
        return () => undefined
      },
      dispatch,
      dispose: simulation.dispose,
    }
    const user = userEvent.setup()
    render(<LabShell adapter={unavailableAdapter} />)

    const addReader = screen.getByRole('button', { name: 'Add reader worker' })
    const addSender = screen.getByRole('button', { name: 'Add sender worker' })
    expect((addReader as HTMLButtonElement).disabled).toBe(true)
    expect((addSender as HTMLButtonElement).disabled).toBe(true)
    await user.click(addReader)
    await user.click(addSender)
    expect(dispatch).not.toHaveBeenCalled()
    unavailableAdapter.dispose()
  })

  it('commits a canvas worker hold once when repeat reaches the worker limit', () => {
    vi.useFakeTimers()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    const view = render(<LabShell adapter={adapter} />)
    dispatch.mockClear()
    const increase = screen.getByRole('button', { name: 'Add reader worker' })
    const count = view.container.querySelector('#reader-count')!

    fireEvent.pointerDown(increase, { pointerId: 1 })
    expect(count.textContent).toBe('6')
    act(() => vi.advanceTimersByTime(539))
    expect(count.textContent).toBe('6')
    expect(dispatch).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(1))
    expect(count.textContent).toBe('7')
    expect(dispatch).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(120))
    expect(dispatch.mock.calls).toEqual([[{ type: 'set-reader-workers', value: 7 }]])

    fireEvent.pointerUp(window, { pointerId: 1 })
    fireEvent.click(increase, { detail: 1 })
    act(() => vi.advanceTimersByTime(1000))
    expect(dispatch).toHaveBeenCalledTimes(1)
  })

  it('accepts keyboard activation after cancelling a canvas worker hold', async () => {
    vi.useFakeTimers()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    const view = render(<LabShell adapter={adapter} />)
    dispatch.mockClear()
    const increase = screen.getByRole('button', { name: 'Add reader worker' })
    const count = view.container.querySelector('#reader-count')!

    fireEvent.pointerDown(increase, { pointerId: 1 })
    expect(count.textContent).toBe('6')
    fireEvent.pointerCancel(window, { pointerId: 1 })
    act(() => vi.advanceTimersByTime(1000))
    expect(count.textContent).toBe('5')
    expect(dispatch).not.toHaveBeenCalled()

    vi.useRealTimers()
    increase.focus()
    await userEvent.setup().keyboard('{Enter}')
    expect(dispatch.mock.calls).toEqual([[{ type: 'set-reader-workers', value: 6 }]])
  })

  it('shows backend batch sizes and queue depths without run or startup controls', async () => {
    adapter = new SimulationAdapter()
    const initial = adapter.getSnapshot()
    let snapshot: LoadgenTelemetrySnapshot = {
      ...initial,
      adapterKind: 'http',
      runState: 'running',
      throttler: {
        ...initial.throttler,
        installed: { ...initial.throttler.installed, applyMode: 'immediate', writable: true },
      },
      reader: {
        ...initial.reader,
        readBatchSize: 1_000,
      },
      readerChannel: {
        ...initial.readerChannel,
        depthBatches: 1,
        capacity: 2,
      },
    }
    const dispatch = vi.fn()
    const listeners = new Set<(value: LoadgenTelemetrySnapshot) => void>()
    const backend: LoadgenAdapter = {
      kind: 'http',
      getSnapshot: () => snapshot,
      subscribe: (listener) => {
        listeners.add(listener)
        listener(snapshot)
        return () => {
          listeners.delete(listener)
        }
      },
      dispatch,
      dispose: adapter.dispose,
    }
    const user = userEvent.setup()
    render(<LabShell adapter={backend} />)

    expect(within(screen.getByRole('banner')).queryAllByRole('button')).toHaveLength(0)
    expect(screen.getAllByRole('slider')).toEqual([
      screen.getByRole('slider', { name: 'Throttle opening' }),
    ])
    expect(screen.getByText('Batch 1,000 tx')).not.toBeNull()
    expect(screen.getByText('Depth 1 / 2 batches')).not.toBeNull()

    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))
    expect(screen.getByRole('spinbutton', { name: /^Workers/ })).not.toBeNull()
    expect(screen.getAllByRole('spinbutton')).toHaveLength(1)
    expect(screen.getByText('Read batch size: 1,000 tx')).not.toBeNull()

    act(() => {
      snapshot = {
        ...snapshot,
        revision: snapshot.revision + 1,
        runState: 'stopping',
        readerChannel: { ...snapshot.readerChannel, depthBatches: 0 },
      }
      for (const listener of listeners) listener(snapshot)
    })
    expect(screen.getByText('stopping')).not.toBeNull()
    expect(screen.getByText('Depth 0 / 2 batches')).not.toBeNull()
    expect(dispatch).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: /Inspect sender/ }))
    const inspector = within(screen.getByRole('complementary'))
    expect(
      inspector.getAllByRole('heading', { level: 3 }).map((heading) => heading.textContent),
    ).toEqual(['Controls', 'Pool'])
    expect(screen.getAllByRole('spinbutton')).toEqual([
      screen.getByRole('spinbutton', { name: /^Workers/ }),
    ])
    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    expect(screen.getAllByRole('spinbutton')).toEqual([
      screen.getByRole('spinbutton', { name: /^Requested TPS/ }),
    ])
    expect(screen.getAllByRole('checkbox')).toEqual([
      screen.getByRole('checkbox', { name: 'Throttler valve installed' }),
    ])
    expect(screen.getByRole('button', { name: 'Remove throttler valve' })).not.toBeNull()
  })

  it('opens a real actor inspector and clears the selection', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Inspect reader' }))

    expect(screen.getByRole('heading', { name: 'READER' })).not.toBeNull()
    expect(screen.getByLabelText('Reader configuration')).not.toBeNull()
    expect(
      screen.getByRole('button', { name: 'Inspect reader' }).getAttribute('aria-pressed'),
    ).toBe('true')

    await user.click(screen.getByRole('button', { name: 'Clear selection' }))
    expect(screen.getByText('No pipeline object selected')).not.toBeNull()
    expect(
      screen.getByRole('button', { name: 'Inspect reader' }).getAttribute('aria-pressed'),
    ).toBe('false')
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
    expect(screen.getByRole('complementary', { name: 'READER' })).not.toBeNull()
    await user.click(reader)
    expect(screen.queryByRole('complementary')).toBeNull()
  })

  it.each([1119, 1440])(
    'keeps the inspector and selection on repeated activation at %i px',
    async (width) => {
      vi.stubGlobal('innerWidth', width)
      const user = userEvent.setup()
      adapter = new SimulationAdapter()
      render(<LabShell adapter={adapter} />)
      const sender = screen.getByRole('button', { name: /^Inspect sender,/ })

      expect(screen.getByRole('complementary', { name: 'INSPECTOR' })).not.toBeNull()
      await user.click(sender)
      await user.click(sender)
      expect(screen.getByRole('complementary', { name: 'SENDER' })).not.toBeNull()
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
    expect(screen.getByRole('complementary', { name: 'THROTTLER' })).not.toBeNull()
    expect(screen.getByRole('slider').getAttribute('aria-valuetext')).toContain('applied')

    const installation = screen.getByRole('button', { name: 'Remove throttler valve' })
    fireEvent.pointerDown(installation, { clientX: 100, clientY: 100, pointerId: 2 })
    fireEvent.pointerMove(installation, { clientX: 100, clientY: 75, pointerId: 2 })
    expect(installation.getAttribute('data-candidate-kind')).toBe('preview')
    await user.keyboard('{Escape}')
    expect(installation.getAttribute('data-candidate-kind')).toBe('')
    expect(screen.getByRole('complementary', { name: 'THROTTLER' })).not.toBeNull()
    expect(dispatch).not.toHaveBeenCalled()
    installation.focus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('complementary')).toBeNull())
    expect(document.activeElement).toBe(installation)
  })

  it('offers the supported canvas and Inspector controls in simulation', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)

    expect(within(screen.getByRole('banner')).queryAllByRole('button')).toHaveLength(0)
    const diagram = within(screen.getByRole('group', { name: /Reader to target/ }))
    const settingsButtons = diagram
      .getAllByRole('button')
      .map((button) => button.getAttribute('aria-label'))
      .filter((label) => !label?.startsWith('Inspect'))
    expect(settingsButtons.sort()).toEqual([
      'Add reader worker',
      'Add sender worker',
      'Remove reader worker',
      'Remove sender worker',
      'Remove throttler valve',
    ])
    expect(diagram.getAllByRole('slider')).toEqual([
      diagram.getByRole('slider', { name: 'Throttle opening' }),
    ])
    expect(diagram.getByRole('group', { name: 'reader workers' })).not.toBeNull()
    expect(diagram.getByRole('group', { name: 'sender workers' })).not.toBeNull()
    expect(diagram.getByText('Batch 1,000 tx')).not.toBeNull()
    expect(diagram.getByText(/^Depth \d+ \/ 4 batches$/)).not.toBeNull()
    expect(diagram.getByText(/^Depth \d+ \/ 100 batches$/)).not.toBeNull()

    const inspector = within(screen.getByRole('complementary'))
    await user.click(diagram.getByRole('button', { name: 'Inspect reader' }))
    expect(inspector.getAllByRole('spinbutton')).toEqual([
      inspector.getByRole('spinbutton', { name: /^Workers/ }),
    ])
    expect(inspector.getByText('Read batch size: 1,000 tx')).not.toBeNull()
    await user.click(diagram.getByRole('button', { name: /Inspect sender,/ }))
    expect(inspector.getAllByRole('spinbutton')).toEqual([
      inspector.getByRole('spinbutton', { name: /^Workers/ }),
    ])
    await user.click(diagram.getByRole('button', { name: 'Inspect throttler' }))
    expect(inspector.getAllByRole('spinbutton')).toEqual([
      inspector.getByRole('spinbutton', { name: /^Requested TPS/ }),
    ])
    expect(inspector.getAllByRole('checkbox')).toEqual([
      inspector.getByRole('checkbox', { name: 'Throttler valve installed' }),
    ])
    expect(inspector.getAllByText('Requested TPS')).toHaveLength(1)
    await user.click(diagram.getByRole('button', { name: /Inspect ingestion service/ }))
    expect(inspector.queryAllByRole('spinbutton')).toHaveLength(0)
    expect(inspector.queryAllByRole('checkbox')).toHaveLength(0)
    expect(inspector.queryAllByRole('slider')).toHaveLength(0)
    await user.click(diagram.getByRole('button', { name: 'Inspect reader to throttler channel' }))
    expect(inspector.getByText(/^\d+ \/ 4 batches$/)).not.toBeNull()
    await user.click(diagram.getByRole('button', { name: 'Inspect throttler to sender channel' }))
    expect(inspector.getByText(/^\d+ \/ 100 batches$/)).not.toBeNull()

    await user.click(diagram.getByRole('button', { name: 'Add reader worker' }))
    await user.click(diagram.getByRole('button', { name: 'Add sender worker' }))
    diagram.getByRole('slider', { name: 'Throttle opening' }).focus()
    await user.keyboard('{End}')
    await waitFor(() => {
      expect(dispatch.mock.calls.map(([command]) => command)).toEqual([
        { type: 'set-reader-workers', value: 6 },
        { type: 'set-sender-workers', value: 4 },
        { type: 'set-requested-tps', value: 250_000 },
      ])
    })
  })

  it('removes the simulation valve from the Inspector and reinstalls it on the canvas', async () => {
    vi.useFakeTimers()
    const simulation = new SimulationAdapter()
    adapter = simulation
    const dispatch = vi.spyOn(simulation, 'dispatch')
    render(<LabShell adapter={simulation} />)

    fireEvent.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    const requestedTps = screen.getByRole('spinbutton', { name: /^Requested TPS/ })
    const valveMode = screen.getByRole<HTMLInputElement>('checkbox', {
      name: 'Throttler valve installed',
    })
    await act(async () => {
      fireEvent.change(requestedTps, { target: { value: '0' } })
      fireEvent.keyDown(requestedTps, { key: 'Enter' })
      vi.advanceTimersByTime(500)
    })
    expect(valveMode.checked).toBe(true)
    const installed = simulation.getSnapshot()
    expect(installed.throttler.installed).toMatchObject({
      applied: true,
      writable: true,
      applyMode: 'immediate',
    })
    expect(installed.throttler.requestedTps.applied).toBe(0)
    expect(installed.senderChannel.sentBatchesTotal).toBe(0)

    await act(async () => {
      fireEvent.click(valveMode)
      vi.advanceTimersByTime(500)
    })
    const bypassed = simulation.getSnapshot()
    expect(valveMode.checked).toBe(false)
    expect(bypassed.throttler.installed.applied).toBe(false)
    expect(bypassed.throttler.requestedTps.applied).toBe(0)
    expect(bypassed.senderChannel.sentBatchesTotal).toBeGreaterThan(0)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Reinsert throttler valve' }))
      vi.advanceTimersByTime(500)
    })
    const reinstalled = simulation.getSnapshot()
    expect(valveMode.checked).toBe(true)
    expect(reinstalled.throttler.installed.applied).toBe(true)
    expect(reinstalled.throttler.requestedTps.applied).toBe(0)
    expect(reinstalled.senderChannel.sentBatchesTotal).toBe(bypassed.senderChannel.sentBatchesTotal)
    expect(dispatch.mock.calls.map(([command]) => command)).toEqual([
      { type: 'set-requested-tps', value: 0 },
      { type: 'set-throttler-installed', value: false },
      { type: 'set-throttler-installed', value: true },
    ])
  })

  it('renders only supported channel states in the legend', () => {
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.queryByText('Connection error')).toBeNull()
    for (const label of ['Normal flow', 'Near limit', 'Backpressure', 'Stopped']) {
      expect(screen.getByText(label)).not.toBeNull()
    }
    const legend = screen.getByRole('contentinfo', { name: 'Channel states' })
    expect(legend.querySelectorAll('i[aria-hidden="true"]')).toHaveLength(4)
  })

  it('renders all Sender sections and keeps long config rows full width', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: /Inspect sender/ }))

    const sectionTitles = screen
      .getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent)
    expect(sectionTitles).toEqual(['Controls', 'Pool', 'Delivery', 'Diagnostics'])
    for (const label of [
      'Workers desired / live / draining',
      'Worker states',
      'Attempted TPS',
      'Retry TPS',
      'Terminal failed TPS',
      'In-flight',
      'Backoff',
      'Attempts',
      'Retry attempts',
      '2xx responses',
      'Rejected responses',
      'Timeouts',
      'Terminal failed batches',
      'Terminal failed transactions',
      'Ambiguous timeout transactions',
      'Duplicate-risk transactions',
      'Ambiguous terminal transactions',
      'Diagnostic interpretation',
    ]) {
      expect(screen.getByText(label)).not.toBeNull()
    }
    expect(screen.getByText('Diagnostic interpretation').parentElement?.className).toContain(
      'inspector-data__row--full-width',
    )
    const workerSummary = screen.getByText('Worker states').parentElement
    expect(workerSummary?.className).toBe(
      'inspector-data__row--full-width inspector-data__row--worker-states',
    )
    expect(workerSummary?.querySelectorAll('.worker-state')).toHaveLength(3)
    expect(screen.getAllByTestId('sender-inspector-section-data')).toHaveLength(3)
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
    expect(screen.getByRole('heading', { name: 'THROTTLER' })).not.toBeNull()
    expect(
      screen.getByRole('button', { name: 'Inspect throttler' }).getAttribute('aria-pressed'),
    ).toBe('true')
    expect(screen.getByText('running')).not.toBeNull()
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
