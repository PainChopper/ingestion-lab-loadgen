import { act, render, screen, waitFor } from '@testing-library/react'
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
})

describe('LabShell', () => {
  it('dispatches the one on-canvas Batch command exactly once per click', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const initial = adapter.getSnapshot()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    const view = render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Increase Batch' }))
    await waitFor(() => expect(dispatch.mock.calls.filter(
      ([command]) => command.type === 'set-read-batch-size',
    )).toHaveLength(1))
    expect(dispatch.mock.calls.find(
      ([command]) => command.type === 'set-read-batch-size',
    )?.[0]).toEqual({
      type: 'set-read-batch-size',
      value: (initial.reader.readBatchSize.applied ??
        initial.reader.readBatchSize.min) + initial.reader.readBatchSize.step,
    })
    expect(view.container.querySelectorAll('.pipeline-batch-stepper'))
      .toHaveLength(1)
    expect(dispatch).toHaveBeenCalledTimes(1)

    await user.click(view.container.querySelector('#sender-actor')!)
    expect(screen.getByLabelText('Sender configuration').children)
      .toHaveLength(1)
  })

  it('renders a Reader source error overlay and clears it on the next snapshot', async () => {
    const simulation = new SimulationAdapter()
    let snapshot: LoadgenTelemetrySnapshot = {
      ...simulation.getSnapshot(),
		runState: 'faulted',
		reader: {
			...simulation.getSnapshot().reader,
			state: 'faulted',
			sourceDirectory: 'C:/dataset', sourceError: { category: 'source', operation: 'read', relativePath: 'broken.parquet', message: 'corrupt parquet', workerId: 2 },
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
		expect(label.parentElement?.className).toContain('inspector-data__row--source-error')
		expect(label.nextElementSibling?.tagName).toBe('DD')
		expect(label.parentElement?.querySelectorAll('dd')).toHaveLength(1)

    act(() => {
			snapshot = { ...snapshot, runState: 'idle', reader: { ...snapshot.reader, state: 'idle', sourceError: null } }
      listeners.forEach((listener) => listener(snapshot))
    })
    expect(screen.queryByRole('alert')).toBeNull()
    errorAdapter.dispose()
  })

  it('keeps desired controls live while paused telemetry remains frozen', async () => {
    const simulation = new SimulationAdapter()
    const running: LoadgenTelemetrySnapshot = {
      ...simulation.getSnapshot(),
      revision: 1,
      runState: 'running',
      reader: { ...simulation.getSnapshot().reader, state: 'running' },
      throttler: { ...simulation.getSnapshot().throttler, state: 'running' },
      sender: {
        ...simulation.getSnapshot().sender,
        state: 'running',
        liveWorkers: 2,
        workerSlots: [
          { workerId: 1, activity: 'in-flight', lifecycle: 'active', terminalError: false },
        ],
      },
    }
    let snapshot = running
    const listeners = new Set<(next: LoadgenTelemetrySnapshot) => void>()
    const controlledAdapter: LoadgenAdapter = {
      kind: 'simulation',
      getSnapshot: () => snapshot,
      subscribe: (listener) => {
        listeners.add(listener)
        listener(snapshot)
        return () => listeners.delete(listener)
      },
      dispatch: vi.fn(),
      dispose: simulation.dispose,
    }
    render(<LabShell adapter={controlledAdapter} />)

    act(() => {
      snapshot = {
        ...running,
        revision: 2,
        runState: 'paused',
        reader: { ...running.reader, state: 'paused' },
        throttler: { ...running.throttler, state: 'paused' },
        sender: {
          ...running.sender,
          state: 'paused',
          workers: { ...running.sender.workers, applied: 7 },
          liveWorkers: 0,
          workerSlots: [],
        },
      }
      listeners.forEach((listener) => listener(snapshot))
    })

    expect(screen.getByText('Paused — last observed telemetry frozen; controls remain desired').textContent)
      .toBe('Paused — last observed telemetry frozen; controls remain desired')
    const qualifier = screen.getByText(
      'Paused — last observed telemetry frozen; controls remain desired',
    )
    expect(document.querySelector('.topbar')).not.toBeNull()
    expect(qualifier.getAttribute('role')).toBe('status')
    await waitFor(() => expect(document.querySelector('#sender-count')?.textContent).toBe('7'))
    expect(document.querySelector('#sender-actor')?.textContent)
      .not.toContain('desired ·')
    expect(document.querySelector('#sender-actor')?.textContent)
      .toContain('0 idle · 1 in-flight · 0 backoff · 0 errors')
    expect(screen.getByRole('button', { name: 'Resume run' })).not.toBeNull()
    await userEvent.setup().click(screen.getByRole('button', { name: 'Inspect reader' }))
    expect(screen.getByText('Workers desired / frozen live / draining')).not.toBeNull()
    await userEvent.setup().click(screen.getByRole('button', { name: /Inspect sender/ }))
    expect(screen.getByText('Workers desired / frozen live / draining')).not.toBeNull()
    controlledAdapter.dispose()
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
    expect(screen.getByRole('button', { name: 'Resume run' })).not.toBeNull()
    expect(screen.queryAllByRole('button', { name: /^(Pause|Resume) run$/ })).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Reset run' }))
    await waitFor(() => {
      expect(screen.getByText('idle')).not.toBeNull()
      expect(screen.getByText('00:00:00')).not.toBeNull()
    })
  })

  it('disables Run when the HTTP policy is unavailable and does not dispatch it', async () => {
    const simulation = new SimulationAdapter()
    const snapshot: LoadgenTelemetrySnapshot = {
      ...simulation.getSnapshot(),
      adapterKind: 'http',
      connectionState: 'error',
      policy: null,
    }
    const dispatch = vi.fn()
    const unavailableAdapter: LoadgenAdapter = {
      kind: 'http',
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

    const run = screen.getByRole('button', { name: 'Start run' })
    expect((run as HTMLButtonElement).disabled).toBe(true)
    await user.click(run)
    expect(dispatch).not.toHaveBeenCalled()
    unavailableAdapter.dispose()
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

  it('owns retained Sender controls and leaves ingestion service without controls', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: /Inspect sender/ }))
    expect(screen.getByLabelText('Sender configuration')).not.toBeNull()
    expect(screen.getByRole('spinbutton', { name: /^Workers/ })).not.toBeNull()
    expect(screen.queryByRole('spinbutton', { name: /^HTTP timeout/ })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Add sender worker' }))
    expect(dispatch).toHaveBeenCalledWith({ type: 'set-sender-workers', value: 4 })

    await user.click(screen.getByRole('button', { name: 'Inspect ingestion service' }))
    expect(screen.queryByLabelText('Sender configuration')).toBeNull()
    expect(screen.queryByRole('spinbutton', { name: /^Workers/ })).toBeNull()
    expect(screen.queryByRole('spinbutton', { name: /^HTTP timeout/ })).toBeNull()
  })

  it('changes Valve mode through the existing throttler command', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    const valveMode = screen.getByRole('combobox', { name: 'Valve mode' })
    await user.selectOptions(valveMode, 'bypass')

    await waitFor(() => {
      expect(dispatch).toHaveBeenCalledWith({
        type: 'set-throttler-installation-mode', value: 'bypass',
      })
      expect((valveMode as HTMLSelectElement).value).toBe('bypass')
    })
  })

  it('renders only supported channel states in the legend', () => {
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    expect(screen.queryByText('Connection error')).toBeNull()
    for (const label of ['Normal flow', 'Near limit', 'Backpressure', 'Stopped']) {
      expect(screen.getByText(label)).not.toBeNull()
    }
  })

  it('renders all Sender sections and keeps long policy rows full width', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: /Inspect sender/ }))

    const sectionTitles = screen.getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent)
    expect(sectionTitles).toEqual([
      'Управление',
      'Pool',
      'Delivery',
      'Diagnostics',
    ])
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
      'Retry policy',
      'Diagnostic interpretation',
    ]) {
      expect(screen.getByText(label)).not.toBeNull()
    }
    expect(screen.getByText('Retry policy').parentElement?.className)
      .toContain('inspector-data__row--full-width')
    expect(screen.getByText('Diagnostic interpretation').parentElement?.className)
      .toContain('inspector-data__row--full-width')
    const workerSummary = screen.getByText('Worker states').parentElement
    expect(workerSummary?.className).toContain('inspector-data__row--full-width')
    expect(workerSummary?.querySelectorAll('.worker-state')).toHaveLength(4)
    expect(screen.getAllByTestId('sender-inspector-section-data'))
      .toHaveLength(3)
  })

  it('shares numeric TPS preview and absolute command with the valve', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)
    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
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

  it('removes and reinserts the valve without changing selection or saved TPS', async () => {
    const user = userEvent.setup()
    adapter = new SimulationAdapter()
    const dispatch = vi.spyOn(adapter, 'dispatch')
    render(<LabShell adapter={adapter} />)

    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
    const remove = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })
    await user.click(remove)

    const reinsert = await screen.findByRole('button', {
      name: 'Reinsert throttler valve',
    })
    expect(dispatch).toHaveBeenCalledWith({
      type: 'set-throttler-installation-mode',
      value: 'bypass',
    })
    expect(reinsert.getAttribute('aria-pressed')).toBe('true')
    expect(document.activeElement).toBe(reinsert)
    expect(screen.getAllByRole('spinbutton', { name: /^Requested TPS/ })
      .every((input) => (input as HTMLInputElement).value === '120000'))
      .toBe(true)
    expect(screen.getByRole('heading', { name: 'THROTTLER' })).not.toBeNull()

    await user.click(reinsert)
    const restored = await screen.findByRole('button', {
      name: 'Remove throttler valve',
    })
    expect(dispatch).toHaveBeenCalledWith({
      type: 'set-throttler-installation-mode',
      value: 'installed',
    })
    expect(restored.getAttribute('aria-pressed')).toBe('false')
    expect(document.activeElement).toBe(restored)
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

    await user.click(screen.getByRole('button', { name: 'Inspect throttler' }))
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
    expect(screen.getByLabelText('Load generator laboratory')
      .getAttribute('data-layout')).toBe('portrait')
    expect(screen.getByRole('heading', { name: 'THROTTLER' })).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Inspect throttler' })
      .getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByText('running')).not.toBeNull()
    expect((requested as HTMLInputElement).value).toBe('135000')
    expect(document.activeElement).toBe(requested)
    expect(document.querySelector('.pipeline-svg')?.getAttribute('viewBox'))
      .toBe('0 0 480 670')
  })
})
