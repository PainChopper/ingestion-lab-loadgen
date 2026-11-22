/// <reference types="node" />

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type { NumericControlSnapshot, ChannelSnapshot, SelectableId } from '../../model/loadgen'
import { ChannelFlowStateDeriver } from '../../model/channelFlowState'
import { createPipelineGeometry } from './geometry'
import type { PipelineOrientation } from './pipelineLayout'
import { ThrottlerActor } from './ThrottlerActor'
import { useDesiredControl } from '../../hooks/useDesiredControl'
import { VALVE_INSTALLATION_CONTROL, VALVE_OPENING_CONTROLS } from './throttlerValve'

const styleElement = document.createElement('style')
const pipelineStyles = readFileSync(
  resolve(process.cwd(), 'src/components/pipeline/PipelineSvg.css'),
  'utf8',
)

beforeAll(() => {
  styleElement.textContent = pipelineStyles
  document.head.append(styleElement)
})

function requestedControl(overrides: Partial<NumericControlSnapshot> = {}): NumericControlSnapshot {
  return {
    applied: 120_000,
    min: 0,
    max: 250_000,
    unit: 'tx/s',
    applyMode: 'immediate',
    ...overrides,
  }
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value)) deepFreeze(child)
    Object.freeze(value)
  }
  return value
}

function createBaseSnapshot() {
  const adapter = new SimulationAdapter()
  try {
    return deepFreeze(new ChannelFlowStateDeriver().derive(adapter.getSnapshot(), 0))
  } finally {
    adapter.dispose()
  }
}

const snapshot = createBaseSnapshot()

interface HarnessSnapshotProps {
  control?: NumericControlSnapshot
  onSelect?: (id: SelectableId) => void
  channelOverrides?: Partial<ChannelSnapshot>
  installed?: boolean | null
  orientation?: PipelineOrientation
}

interface HarnessOptions extends HarnessSnapshotProps {
  accepted?: boolean
  onCommand?: (value: number) => void
  modeAccepted?: boolean
  confirmRequested?: boolean
  confirmMode?: boolean
  onModeCommand?: (value: boolean) => void
}

function Harness({
  control = requestedControl(),
  onSelect = () => undefined,
  channelOverrides = {},
  installed = true,
  orientation = 'landscape',
  revision,
  dispatchRequested,
  dispatchInstalled,
}: HarnessSnapshotProps & {
  revision: number
  dispatchRequested: (value: number) => Promise<{ accepted: boolean }>
  dispatchInstalled: (value: boolean) => Promise<{ accepted: boolean }>
}) {
  const requestedApplied = control.applied ?? control.min
  const requestedController = useDesiredControl({
    applied: requestedApplied,
    revision,
    available: control.applied !== null && control.applyMode !== 'unavailable',
    dispatch: dispatchRequested,
  })
  const modeController = useDesiredControl({
    applied: installed ?? true,
    revision,
    available: installed !== null,
    dispatch: dispatchInstalled,
    rejectionMessage: 'Valve mode change rejected',
    unavailableMessage: 'Valve mode change unavailable',
  })
  const geometry = createPipelineGeometry({
    orientation,
    readerWorkers: 7,
    senderWorkers: 32,
  })

  return (
    <svg>
      <ThrottlerActor
        snapshot={{
          ...snapshot.throttler,
          requestedTps: { ...control, applied: requestedApplied },
          installed: {
            ...snapshot.throttler.installed,
            applied: installed,
          },
        }}
        upstreamChannel={{ ...snapshot.readerChannel, ...channelOverrides }}
        requestedTpsControl={requestedController}
        installedControl={modeController}
        selected={false}
        onSelect={onSelect}
        geometry={geometry.actors.throttler}
        orientation={orientation}
      />
    </svg>
  )
}

function renderHarness(initialProps: HarnessOptions = {}) {
  let props = initialProps
  let revision = snapshot.revision

  function dispatchRequested(value: number) {
    const { onCommand, accepted = true } = props
    onCommand?.(value)
    if (accepted && props.confirmRequested !== false) {
      rerender({ ...props, control: { ...(props.control ?? requestedControl()), applied: value } })
    }
    return Promise.resolve({ accepted })
  }

  async function dispatchInstalled(value: boolean) {
    const { onModeCommand, modeAccepted = true } = props
    onModeCommand?.(value)
    if (modeAccepted && props.confirmMode !== false) rerender({ ...props, installed: value })
    return { accepted: modeAccepted }
  }

  function element() {
    return (
      <Harness
        {...props}
        revision={revision}
        dispatchRequested={dispatchRequested}
        dispatchInstalled={dispatchInstalled}
      />
    )
  }

  const view = render(element())
  function rerender(nextProps: HarnessOptions) {
    props = nextProps
    revision += 1
    view.rerender(element())
  }

  return { ...view, rerender }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ThrottlerActor valve control', () => {
  it('steps from either pointer half without activating inspection', async () => {
    const onCommand = vi.fn()
    const onSelect = vi.fn()
    const view = renderHarness({ onCommand, onSelect })
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })

    expect(slider.getAttribute('aria-valuenow')).toBe('2')
    expect(slider.getAttribute('aria-valuetext')).toContain('36% open')
    expect(view.container.querySelector('.pipeline-valve-body')).not.toBeNull()
    expect(view.container.querySelectorAll('.pipeline-valve-wheel-knob')).toHaveLength(6)
    expect(view.container.querySelectorAll('.pipeline-valve-wheel-spoke')).toHaveLength(5)
    expect(view.container.querySelector('[data-direction="decrease"]')?.getAttribute('x')).toBe(
      String(VALVE_OPENING_CONTROLS.decrease.x),
    )
    expect(view.container.querySelector('[data-direction="increase"]')?.getAttribute('x')).toBe(
      String(VALVE_OPENING_CONTROLS.increase.x),
    )
    expect(
      view.container.querySelector('[data-installation-grip="wheel"]')?.getAttribute('height'),
    ).toBe(String(VALVE_INSTALLATION_CONTROL.installedTarget.height))
    fireEvent.pointerDown(view.container.querySelector('[data-direction="increase"]')!)
    fireEvent.pointerUp(window)

    expect(onCommand).toHaveBeenCalledWith(150_000)
    await waitFor(() => expect(slider.getAttribute('aria-valuenow')).toBe('3'))
    expect(onSelect).not.toHaveBeenCalled()

    fireEvent.pointerDown(view.container.querySelector('[data-direction="decrease"]')!)
    fireEvent.pointerUp(window)
    expect(onCommand).toHaveBeenLastCalledWith(100_000)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Inspect throttler' }))
    expect(onSelect).toHaveBeenCalledWith('throttler')
  })

  it.each([
    { max: 250_001, last: 250_001 },
    { max: 1_000_000_000_000_000, last: 1_000_000_000_000_000 },
    { max: 1, last: 1 },
    { max: 10, last: 10 },
    { max: Number.MAX_SAFE_INTEGER, last: Number.MAX_SAFE_INTEGER },
  ])('sends the exact maximum on End for max=$max', async ({ max, last }) => {
    const onCommand = vi.fn()
    renderHarness({ control: requestedControl({ applied: 0, max }), onCommand })
    const slider = screen.getByRole('slider')
    slider.focus()
    await userEvent.setup().keyboard('{End}')
    expect(onCommand).toHaveBeenCalledExactlyOnceWith(last)
    expect(slider.getAttribute('aria-valuenow')).toBe(slider.getAttribute('aria-valuemax'))
    expect(slider.getAttribute('aria-valuetext')).toContain('100% open')
    await userEvent.setup().keyboard('{Home}')
    expect(onCommand).toHaveBeenLastCalledWith(0)
  })

  it.each([
    { applied: 1, target: 0, action: 'Home' },
    { applied: 3_999_999, target: 4_000_000, action: 'pointer' },
  ])(
    'reaches $target from $applied with $action without repeating it',
    async ({ applied, target, action }) => {
      const onCommand = vi.fn()
      const control = requestedControl({ applied, max: 4_000_000 })
      const view = renderHarness({ control, onCommand, confirmRequested: false })
      const slider = screen.getByRole('slider', { name: 'Throttle opening' })
      const activate = () => {
        if (action === 'pointer') {
          fireEvent.pointerDown(view.container.querySelector('[data-wheel-direction="increase"]')!)
          fireEvent.pointerUp(window)
        } else {
          fireEvent.keyDown(slider, { key: action })
        }
      }

      activate()
      await waitFor(() => expect(onCommand).toHaveBeenCalledExactlyOnceWith(target))
      expect(slider.getAttribute('aria-disabled')).toBe('true')
      activate()
      expect(onCommand).toHaveBeenCalledTimes(1)

      view.rerender({ control: { ...control, applied: target }, onCommand })
      expect(slider.getAttribute('aria-disabled')).toBe('false')
      activate()
      expect(onCommand).toHaveBeenCalledTimes(1)
    },
  )

  it('uses visible sides for the current config without issuing bypass commands', async () => {
    const onCommand = vi.fn()
    const onModeCommand = vi.fn()
    const view = renderHarness({
      control: requestedControl({
        applied: 1_800_000,
        max: 4_000_000,
      }),
      onCommand,
      onModeCommand,
    })
    const decrease = view.container.querySelector('[data-direction="decrease"]')!
    const increase = view.container.querySelector('[data-direction="increase"]')!

    fireEvent.pointerDown(decrease)
    fireEvent.pointerUp(window)
    await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
    fireEvent.pointerDown(increase)
    fireEvent.pointerUp(window)

    expect(onCommand.mock.calls.map(([value]) => value)).toEqual([1_500_000, 2_000_000])
    expect(onModeCommand).not.toHaveBeenCalled()
  })

  it('routes wheel halves through TPS controls without issuing bypass commands', async () => {
    const onCommand = vi.fn()
    const onModeCommand = vi.fn()
    const view = renderHarness({ onCommand, onModeCommand })
    const decrease = view.container.querySelector('[data-wheel-direction="decrease"]')!
    const increase = view.container.querySelector('[data-wheel-direction="increase"]')!

    expect(getComputedStyle(decrease).fill).toBe('rgba(0, 0, 0, 0)')
    expect(getComputedStyle(increase).fill).toBe('rgba(0, 0, 0, 0)')
    fireEvent.pointerDown(decrease)
    fireEvent.pointerUp(window)
    await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
    fireEvent.pointerDown(increase)
    fireEvent.pointerUp(window)
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    slider.focus()
    fireEvent.keyDown(slider, { key: 'ArrowRight' })

    expect(onCommand.mock.calls.map(([value]) => value)).toEqual([50_000, 100_000, 150_000])
    expect(onModeCommand).not.toHaveBeenCalled()
    for (const half of [decrease, increase]) {
      expect(half.getAttribute('aria-hidden')).toBe('true')
      expect(half.hasAttribute('role')).toBe(false)
      expect(half.hasAttribute('tabindex')).toBe(false)
    }
  })

  it('implements bounded keyboard steps without duplicate commands', async () => {
    const user = userEvent.setup()
    const onCommand = vi.fn()
    renderHarness({ control: requestedControl({ applied: 0 }), onCommand })
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)

    await user.keyboard('{ArrowLeft}')
    expect(onCommand).not.toHaveBeenCalled()
    expect(slider.getAttribute('data-wheel-phase')).toBe('0')

    await user.keyboard('{End}{End}')
    expect(onCommand).toHaveBeenCalledTimes(1)
    expect(onCommand).toHaveBeenLastCalledWith(250_000)
    await waitFor(() => expect(slider.getAttribute('aria-valuenow')).toBe('5'))
    expect(slider.getAttribute('data-wheel-phase')).toBe('5')

    await user.keyboard('{Home}')
    await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(2))
    expect(onCommand).toHaveBeenLastCalledWith(0)
    expect(slider.getAttribute('aria-valuenow')).toBe('0')
    expect(slider.getAttribute('data-wheel-phase')).toBe('0')
  })

  it('retries the same opening step after a synchronous dispatch failure', async () => {
    const user = userEvent.setup()
    const onCommand = vi.fn().mockImplementationOnce(() => {
      throw new Error('Dispatch unavailable')
    })
    renderHarness({ control: requestedControl({ applied: 0 }), onCommand })
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)

    const firstOpening = 50_000
    await user.keyboard('{ArrowRight}')
    expect(onCommand.mock.calls).toEqual([[firstOpening]])
    expect(slider.getAttribute('aria-valuenow')).toBe('0')
    expect(slider.getAttribute('data-wheel-phase')).toBe('0')

    await user.keyboard('{ArrowRight}')
    expect(onCommand.mock.calls).toEqual([[firstOpening], [firstOpening]])
    expect(slider.getAttribute('aria-valuenow')).toBe('1')
    expect(slider.getAttribute('data-wheel-phase')).toBe('1')
  })

  it('applies both commands on the mounted actor without React warnings', async () => {
    const consoleError = vi.spyOn(console, 'error')
    const consoleWarn = vi.spyOn(console, 'warn')
    const onCommand = vi.fn()
    const onModeCommand = vi.fn()

    try {
      const view = renderHarness({
        control: requestedControl({ applied: 0 }),
        onCommand,
        onModeCommand,
      })
      const actor = view.container.querySelector('#throttler-actor')
      const slider = screen.getByRole('slider', { name: 'Throttle opening' })
      const mode = screen.getByRole('button', { name: 'Remove throttler valve' })
      await userEvent.setup().click(slider)

      fireEvent.keyDown(slider, { key: 'ArrowRight' })
      expect(onCommand.mock.calls).toEqual([[50_000]])
      screen.getByText('18% OPEN')
      expect(view.container.querySelector('.pipeline-valve-ghost')).toBeNull()

      view.rerender({ control: requestedControl({ applied: 25_000 }), onCommand, onModeCommand })
      expect(view.container.querySelector('#throttler-actor')).toBe(actor)
      expect(screen.getByRole('slider', { name: 'Throttle opening' })).toBe(slider)
      expect(document.activeElement).toBe(slider)

      fireEvent.keyDown(mode, { key: 'Enter' })
      expect(onModeCommand.mock.calls).toEqual([[false]])
      expect(screen.getByRole('button', { name: 'Reinsert throttler valve' })).toBe(mode)
      expect(mode.getAttribute('data-applied-mode')).toBe('bypass')
      expect(document.activeElement).toBe(mode)
      expect(consoleError).not.toHaveBeenCalled()
      expect(consoleWarn).not.toHaveBeenCalled()
    } finally {
      consoleError.mockRestore()
      consoleWarn.mockRestore()
    }
  })

  it('pulls the wheel center upward past a threshold and cancels short pulls', () => {
    const onModeCommand = vi.fn()
    const view = renderHarness({ onModeCommand })
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })
    const grip = view.container.querySelector('[data-installation-grip="wheel"]')!

    fireEvent.pointerDown(grip, { clientX: 100, clientY: 100, pointerId: 1 })
    fireEvent.pointerMove(control, { clientX: 100, clientY: 75, pointerId: 1 })
    expect(
      view.container.querySelector('.pipeline-valve-installation-ghost--preview'),
    ).not.toBeNull()
    fireEvent.pointerUp(control, { clientX: 100, clientY: 75, pointerId: 1 })
    expect(onModeCommand).not.toHaveBeenCalled()
    expect(view.container.querySelector('.pipeline-valve-installation-ghost')).toBeNull()

    fireEvent.pointerDown(grip, { clientX: 100, clientY: 100, pointerId: 2 })
    fireEvent.pointerMove(control, { clientX: 100, clientY: 45, pointerId: 2 })
    fireEvent.pointerUp(control, { clientX: 100, clientY: 45, pointerId: 2 })
    expect(onModeCommand).toHaveBeenCalledOnce()
    expect(onModeCommand).toHaveBeenCalledWith(false)
  })

  it('reinserts the detached assembly with the opposite down-left pull', () => {
    const onModeCommand = vi.fn()
    renderHarness({ installed: false, onModeCommand })
    const control = screen.getByRole('button', {
      name: 'Reinsert throttler valve',
      description: 'bypass applied',
    })

    expect(control.hasAttribute('aria-pressed')).toBe(false)
    fireEvent.pointerDown(control, {
      clientX: 140,
      clientY: 80,
      pointerId: 3,
    })
    fireEvent.pointerMove(control, {
      clientX: 90,
      clientY: 130,
      pointerId: 3,
    })
    fireEvent.pointerUp(control, {
      clientX: 90,
      clientY: 130,
      pointerId: 3,
    })

    expect(onModeCommand).toHaveBeenCalledOnce()
    expect(onModeCommand).toHaveBeenCalledWith(true)
  })

  it('hides saved requested TPS only while bypass is applied', () => {
    const control = requestedControl({ applied: 135_000 })
    const view = renderHarness({ control, installed: false })
    const actor = view.container.querySelector('#throttler-actor')

    expect(actor?.textContent).not.toContain('Requested TPS')
    expect(actor?.textContent).not.toContain('135,000 tx/s')
    expect(actor?.textContent).toContain('Admitted TPS')

    view.rerender({ control, installed: true })
    expect(actor?.textContent).toContain('Requested TPS')
    expect(actor?.textContent).toContain('135,000 tx/s')
  })

  it('cancels removal preview on Escape pointercancel and window blur', () => {
    const onModeCommand = vi.fn()
    const view = renderHarness({ onModeCommand })
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })
    const grip = view.container.querySelector('[data-installation-grip="wheel"]')!
    const begin = (pointerId: number) => {
      fireEvent.pointerDown(grip, {
        clientX: 100,
        clientY: 100,
        pointerId,
      })
      expect(
        view.container.querySelector('.pipeline-valve-installation-ghost--preview'),
      ).not.toBeNull()
    }

    begin(4)
    fireEvent.keyDown(control, { key: 'Escape' })
    expect(view.container.querySelector('.pipeline-valve-installation-ghost')).toBeNull()
    begin(5)
    fireEvent.pointerCancel(control, { pointerId: 5 })
    expect(view.container.querySelector('.pipeline-valve-installation-ghost')).toBeNull()
    begin(6)
    fireEvent.blur(window)
    expect(view.container.querySelector('.pipeline-valve-installation-ghost')).toBeNull()
    expect(onModeCommand).not.toHaveBeenCalled()
  })

  it('supports click and keyboard fallback once and keeps focus after rejection', async () => {
    const user = userEvent.setup()
    const onModeCommand = vi.fn()
    const view = renderHarness({ modeAccepted: false, onModeCommand })
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })

    fireEvent.keyDown(control, { key: 'Enter', repeat: true })
    expect(onModeCommand).not.toHaveBeenCalled()
    await user.click(control)
    await waitFor(() => {
      expect(onModeCommand).toHaveBeenCalledOnce()
      expect(document.activeElement).toBe(control)
      screen.getByText('Valve mode change rejected')
    })
    view.rerender({ installed: null, onModeCommand })
    expect(control.getAttribute('aria-disabled')).toBe('true')
    expect(control.getAttribute('data-applied-mode')).toBe('unavailable')
    expect(control.hasAttribute('aria-valuetext')).toBe(false)
    expect(
      screen.getByRole('button', {
        name: 'Remove throttler valve',
        description: 'unavailable applied',
      }),
    ).toBe(control)
    expect(view.container.querySelector('.pipeline-valve-detached-assembly--applied')).toBeNull()
    await user.click(control)
    expect(onModeCommand).toHaveBeenCalledOnce()
  })

  it('renders live pending mode as a yellow ghost without changing applied state', async () => {
    const view = renderHarness({ confirmMode: false })
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })

    await userEvent.setup().click(control)
    expect(control.hasAttribute('aria-pressed')).toBe(false)
    expect(control.hasAttribute('aria-valuetext')).toBe(false)
    expect(
      screen.getByRole('button', {
        name: 'Remove throttler valve',
        description: 'bypass pending; installed applied',
      }),
    ).toBe(control)
    expect(
      view.container.querySelector('.pipeline-valve-installation-ghost--pending'),
    ).not.toBeNull()
    expect(view.container.querySelector('.pipeline-valve-detached-assembly--applied')).toBeNull()
  })

  it('clears a rejected ghost and keeps invalid capabilities readonly', async () => {
    const user = userEvent.setup()
    const rejected = vi.fn()
    const view = renderHarness({
      control: requestedControl({ applied: 0 }),
      accepted: false,
      onCommand: rejected,
    })
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)
    await user.keyboard('{ArrowRight}')
    await waitFor(() => {
      expect(slider.getAttribute('aria-valuenow')).toBe('0')
      expect(view.container.querySelector('.pipeline-valve-ghost')).toBeNull()
    })

    view.rerender({
      control: requestedControl({ applied: 0, max: 0 }),
      onCommand: rejected,
    })
    const readonly = screen.getByRole('slider', { name: 'Throttle opening' })
    expect(readonly.getAttribute('aria-disabled')).toBe('true')
    await user.click(readonly)
    await user.keyboard('{ArrowRight}{PageUp}{End}')
    expect(rejected).toHaveBeenCalledTimes(1)
  })

  it.each([1, 10, 250_001])('reaches both edges by holding the pointer for max=%s', (max) => {
    vi.useFakeTimers()
    const onCommand = vi.fn()
    const view = renderHarness({ control: requestedControl({ applied: 0, max }), onCommand })
    for (const [direction, value] of [
      ['increase', max],
      ['decrease', 0],
    ] as const) {
      fireEvent.pointerDown(view.container.querySelector(`[data-direction="${direction}"]`)!)
      act(() => vi.advanceTimersByTime(4_000))
      fireEvent.pointerUp(window)
      expect(onCommand).toHaveBeenLastCalledWith(value)
    }
  })

  it('repeats a held pointer step and stops after release', () => {
    vi.useFakeTimers()
    const onCommand = vi.fn()
    const view = renderHarness({ control: requestedControl({ applied: 0 }), onCommand })
    const increase = view.container.querySelector('[data-direction="increase"]')!

    fireEvent.pointerDown(increase)
    act(() => vi.advanceTimersByTime(700))
    fireEvent.pointerUp(window)
    const callsAtRelease = onCommand.mock.calls.length

    expect(onCommand.mock.calls.map(([value]) => value)).toEqual([150_000])
    act(() => vi.advanceTimersByTime(1_000))
    expect(onCommand).toHaveBeenCalledTimes(callsAtRelease)
  })

  it.each(['release', 'pointercancel', 'blur', 'Escape', 'unmount'] as const)(
    'ends a hold at the opening limit on %s',
    (end) => {
      vi.useFakeTimers()
      const onCommand = vi.fn()
      const view = renderHarness({ control: requestedControl({ applied: 155_000 }), onCommand })
      const increase = view.container.querySelector('[data-wheel-direction="increase"]')!
      const slider = screen.getByRole('slider', { name: 'Throttle opening' })

      fireEvent.pointerDown(increase, { pointerId: 1 })
      expect(slider.getAttribute('aria-valuenow')).toBe('4')
      act(() => vi.advanceTimersByTime(780))
      expect(slider.getAttribute('aria-valuenow')).toBe('5')
      expect(onCommand).not.toHaveBeenCalled()

      switch (end) {
        case 'release':
          fireEvent.pointerUp(window, { pointerId: 1 })
          break
        case 'pointercancel':
          fireEvent.pointerCancel(window, { pointerId: 1 })
          break
        case 'blur':
          fireEvent.blur(window)
          break
        case 'Escape':
          fireEvent.keyDown(window, { key: 'Escape' })
          break
        case 'unmount':
          view.unmount()
          break
      }

      act(() => vi.advanceTimersByTime(1_000))
      fireEvent.pointerUp(window, { pointerId: 1 })
      expect(onCommand.mock.calls).toEqual(end === 'release' ? [[250_000]] : [])
      if (end !== 'unmount') {
        expect(slider.getAttribute('aria-valuenow')).toBe(end === 'release' ? '5' : '3')
        expect(view.container.querySelector('[data-gate-kind="preview"]')).toBeNull()
      }
    },
  )

  it('keeps the wheel outline fixed and knobs inside its bounds across phases', () => {
    const view = renderHarness({ control: requestedControl({ applied: 0 }) })
    const outlines: string[] = []
    const phases: string[] = []
    for (const applied of [0, 50_000, 100_000, 150_000, 200_000, 250_000]) {
      view.rerender({ control: requestedControl({ applied }) })
      const rim = view.container.querySelector('.pipeline-valve-wheel-rim')!
      outlines.push(['cx', 'cy', 'rx', 'ry'].map((key) => rim.getAttribute(key)).join(':'))
      phases.push(
        view.container
          .querySelector('.pipeline-valve-wheel-inner-motion')!
          .getAttribute('data-internal-phase')!,
      )
      for (const knob of view.container.querySelectorAll('.pipeline-valve-wheel-knob')) {
        const x = Number(knob.getAttribute('cx'))
        const y = Number(knob.getAttribute('cy'))
        const radius = Number(knob.getAttribute('r'))
        expect(x - radius).toBeGreaterThanOrEqual(400.25)
        expect(x + radius).toBeLessThanOrEqual(459.75)
        expect(y - radius).toBeGreaterThanOrEqual(336.75)
        expect(y + radius).toBeLessThanOrEqual(363.25)
      }
    }
    expect(new Set(outlines)).toHaveLength(1)
    expect(new Set(phases)).toHaveLength(6)
  })

  it('shares perspective and clips closed partial open and ghost pistons', () => {
    const view = renderHarness({ control: requestedControl({ applied: 0 }) })
    const readPose = () =>
      Number(
        view.container.querySelector('[data-gate-kind="applied"]')!.getAttribute('data-gate-y'),
      )
    const aperture = view.container.querySelector('.pipeline-valve-aperture')!
    const body = view.container.querySelector('.pipeline-valve-body')!
    const gate = view.container.querySelector('[data-gate-kind="applied"]')!
    const gateEllipse = gate.querySelector('ellipse')!
    const hits = [...view.container.querySelectorAll('.pipeline-valve-hit-area')]

    expect(aperture.getAttribute('data-axis-ratio')).toBe('0.82')
    expect(
      Number(gateEllipse.getAttribute('ry')) / Number(gateEllipse.getAttribute('rx')),
    ).toBeCloseTo(0.82, 8)
    expect(gate.getAttribute('data-piston-axis-ratio')).toBe('0.82')
    expect(gate.getAttribute('clip-path')).toBe('url(#throttler-valve-aperture-clip)')
    expect(gate.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING).not.toBe(0)
    expect(readPose()).toBe(415)
    expect(body.getAttribute('data-visible-bounds')).toBe('400.25 336.75 59.5 95.25')
    expect(
      hits.map((hit) => [
        hit.getAttribute('x'),
        hit.getAttribute('y'),
        hit.getAttribute('width'),
        hit.getAttribute('height'),
      ]),
    ).toEqual([
      ['394', '398', '26', '34'],
      ['440', '398', '26', '34'],
    ])

    view.rerender({ control: requestedControl({ applied: 100_000 }) })
    const partialY = readPose()
    expect(partialY).toBeGreaterThan(393)
    expect(partialY).toBeLessThan(415)
    expect(partialY + Number(gateEllipse.getAttribute('ry'))).toBeLessThan(415 + 12.3)

    view.rerender({ control: requestedControl({ applied: 250_000 }) })
    expect(readPose()).toBe(393)
    const openEdge = readPose() + Number(gateEllipse.getAttribute('ry')) - (415 - 12.3)
    expect(openEdge).toBeGreaterThan(0)
    expect(openEdge).toBeLessThan(1)
    expect(
      [...view.container.querySelectorAll('[data-gate-kind]')].every(
        (item) => item.getAttribute('clip-path') === 'url(#throttler-valve-aperture-clip)',
      ),
    ).toBe(true)

    view.rerender({ control: requestedControl({ applied: 100_000 }), confirmRequested: false })
    const pendingSlider = screen.getByRole('slider', { name: 'Throttle opening' })
    fireEvent.keyDown(pendingSlider, { key: 'ArrowRight' })
    const ghost = view.container.querySelector('[data-gate-kind="pending"]')!
    expect(ghost.getAttribute('data-piston-axis-ratio')).toBe('0.82')
    expect(
      Number(ghost.querySelector('ellipse')!.getAttribute('ry')) /
        Number(ghost.querySelector('ellipse')!.getAttribute('rx')),
    ).toBeCloseTo(0.82, 8)
  })

  it('leaves an exact flange connector gap and colors it from applied flow only', () => {
    const control = requestedControl({ applied: 0 })
    const view = renderHarness({
      control,
      channelOverrides: { flowState: 'normal', displayedPressure: 0 },
    })
    const connectors = [...view.container.querySelectorAll('.pipeline-valve-flow-line')]
    expect(
      connectors.map((line) => [
        line.getAttribute('data-connector-side'),
        Number(line.getAttribute('x1')),
        Number(line.getAttribute('x2')),
      ]),
    ).toEqual([
      ['left', 355, 401],
      ['right', 459, 505],
    ])
    expect(
      connectors.every(
        (line) => Number(line.getAttribute('x2')) <= 401 || Number(line.getAttribute('x1')) >= 459,
      ),
    ).toBe(true)
    const actor = view.container.querySelector<SVGGElement>('#throttler-actor')!
    expect(actor.style.getPropertyValue('--pipeline-valve-flow-color')).toBe('#79d957')

    view.rerender({
      control,
      channelOverrides: { flowState: 'backpressure', displayedPressure: 1 },
    })
    expect(actor.style.getPropertyValue('--pipeline-valve-flow-color')).toBe('#ff6748')
  })
})
