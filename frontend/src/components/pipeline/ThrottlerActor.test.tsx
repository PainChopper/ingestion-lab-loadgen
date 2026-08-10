import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SimulationAdapter } from '../../adapters/SimulationAdapter'
import type {
  NumericControlSnapshot,
  QueueSnapshot,
  SelectableId,
  ThrottlerInstallationMode,
} from '../../model/loadgen'
import { QueueFlowStateDeriver } from '../../model/queueFlowState'
import { ThrottlerActor } from './ThrottlerActor'

function requestedControl(
  overrides: Partial<NumericControlSnapshot> = {},
): NumericControlSnapshot {
  return {
    applied: 120_000,
    preview: null,
    pending: null,
    min: 0,
    max: 250_000,
    step: 5_000,
    unit: 'tx/s',
    applyMode: 'immediate',
    ...overrides,
  }
}

function Harness({
  control = requestedControl(),
  accepted = true,
  onCommand = vi.fn(),
  onSelect = vi.fn(),
  queueOverrides = {},
  installationMode = 'installed',
  pendingInstallationMode = null,
  modeAccepted = true,
  onModeCommand = vi.fn(),
}: {
  control?: NumericControlSnapshot
  accepted?: boolean
  onCommand?: (value: number) => void
  onSelect?: (id: SelectableId) => void
  queueOverrides?: Partial<QueueSnapshot>
  installationMode?: ThrottlerInstallationMode
  pendingInstallationMode?: ThrottlerInstallationMode | null
  modeAccepted?: boolean
  onModeCommand?: (value: ThrottlerInstallationMode) => void
}) {
  const adapter = new SimulationAdapter()
  const snapshot = new QueueFlowStateDeriver().derive(adapter.getSnapshot(), 0)
  adapter.dispose()
  const [preview, setPreview] = useState<number | null>(null)

  return (
    <svg>
      <ThrottlerActor
        snapshot={{
          ...snapshot.throttler,
          requestedTps: control,
          installationMode: {
            ...snapshot.throttler.installationMode,
            applied: installationMode,
            pending: pendingInstallationMode,
          },
        }}
        upstreamQueue={{ ...snapshot.queue1, ...queueOverrides }}
        previewTps={preview}
        selected={false}
        onSelect={onSelect}
        onPreviewTpsChange={setPreview}
        onRequestedTpsChange={async (value) => {
          onCommand(value)
          return accepted
        }}
        onInstallationModeChange={async (value) => {
          onModeCommand(value)
          return modeAccepted
        }}
      />
    </svg>
  )
}

afterEach(() => {
  vi.useRealTimers()
})

describe('ThrottlerActor valve control', () => {
  it('steps from either pointer half without activating inspection', async () => {
    const onCommand = vi.fn()
    const onSelect = vi.fn()
    const view = render(<Harness onCommand={onCommand} onSelect={onSelect} />)
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })

    expect(slider.getAttribute('aria-valuenow')).toBe('5')
    expect(slider.getAttribute('aria-valuetext')).toContain('45% open')
    expect(view.container.querySelector('.pipeline-valve-body')).not.toBeNull()
    expect(view.container.querySelectorAll('.pipeline-valve-wheel-knob'))
      .toHaveLength(6)
    expect(view.container.querySelectorAll('.pipeline-valve-wheel-spoke'))
      .toHaveLength(5)
    fireEvent.pointerDown(view.container.querySelector('[data-direction="increase"]')!)
    fireEvent.pointerUp(window)

    expect(onCommand).toHaveBeenCalledWith(135_000)
    expect(slider.getAttribute('aria-valuenow')).toBe('6')
    expect(slider.getAttribute('data-wheel-phase')).toBe('1')
    expect(view.container.querySelector('.pipeline-valve-ghost--preview')).not.toBeNull()
    expect(onSelect).not.toHaveBeenCalled()

    fireEvent.pointerDown(view.container.querySelector('[data-direction="decrease"]')!)
    fireEvent.pointerUp(window)
    expect(onCommand).toHaveBeenLastCalledWith(115_000)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Inspect throttler' }))
    expect(onSelect).toHaveBeenCalledWith('throttler')
  })

  it('implements bounded keyboard steps and six cyclic wheel phases', async () => {
    const user = userEvent.setup()
    const onCommand = vi.fn()
    render(
      <Harness
        control={requestedControl({ applied: 0 })}
        onCommand={onCommand}
      />,
    )
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)

    await user.keyboard('{ArrowLeft}')
    expect(onCommand).not.toHaveBeenCalled()
    expect(slider.getAttribute('data-wheel-phase')).toBe('0')

    await user.keyboard('{End}{End}')
    expect(onCommand).toHaveBeenCalledTimes(1)
    expect(onCommand).toHaveBeenLastCalledWith(250_000)
    expect(slider.getAttribute('aria-valuenow')).toBe('11')
    expect(slider.getAttribute('data-wheel-phase')).toBe('5')

    await user.keyboard('{Home}')
    expect(onCommand).toHaveBeenLastCalledWith(0)
    expect(slider.getAttribute('aria-valuenow')).toBe('0')
    expect(slider.getAttribute('data-wheel-phase')).toBe('0')
  })

  it('pulls the wheel center upward past a threshold and cancels short pulls', () => {
    const onModeCommand = vi.fn()
    const view = render(<Harness onModeCommand={onModeCommand} />)
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })
    const grip = view.container.querySelector(
      '[data-installation-grip="wheel"]',
    )!

    fireEvent.pointerDown(grip, { clientX: 100, clientY: 100, pointerId: 1 })
    fireEvent.pointerMove(control, { clientX: 100, clientY: 75, pointerId: 1 })
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost--preview',
    )).not.toBeNull()
    fireEvent.pointerUp(control, { clientX: 100, clientY: 75, pointerId: 1 })
    expect(onModeCommand).not.toHaveBeenCalled()
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost',
    )).toBeNull()

    fireEvent.pointerDown(grip, { clientX: 100, clientY: 100, pointerId: 2 })
    fireEvent.pointerMove(control, { clientX: 100, clientY: 45, pointerId: 2 })
    fireEvent.pointerUp(control, { clientX: 100, clientY: 45, pointerId: 2 })
    expect(onModeCommand).toHaveBeenCalledOnce()
    expect(onModeCommand).toHaveBeenCalledWith('bypass')
  })

  it('reinserts the detached assembly with the opposite down-left pull', () => {
    const onModeCommand = vi.fn()
    render(
      <Harness
        installationMode="bypass"
        onModeCommand={onModeCommand}
      />,
    )
    const control = screen.getByRole('button', {
      name: 'Reinsert throttler valve',
    })

    expect(control.getAttribute('aria-pressed')).toBe('true')
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
    expect(onModeCommand).toHaveBeenCalledWith('installed')
  })

  it('hides saved requested TPS only while bypass is applied', () => {
    const view = render(
      <Harness
        control={requestedControl({ applied: 135_000 })}
        installationMode="bypass"
      />,
    )
    const actor = view.container.querySelector('#throttler-actor')

    expect(actor?.textContent).not.toContain('Requested TPS')
    expect(actor?.textContent).not.toContain('135,000 tx/s')
    expect(actor?.textContent).toContain('Admitted TPS')

    view.rerender(
      <Harness
        control={requestedControl({ applied: 135_000 })}
        installationMode="installed"
      />,
    )
    expect(actor?.textContent).toContain('Requested TPS')
    expect(actor?.textContent).toContain('135,000 tx/s')
  })

  it('cancels removal preview on Escape pointercancel and window blur', () => {
    const onModeCommand = vi.fn()
    const view = render(<Harness onModeCommand={onModeCommand} />)
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })
    const grip = view.container.querySelector(
      '[data-installation-grip="wheel"]',
    )!
    const begin = (pointerId: number) => {
      fireEvent.pointerDown(grip, {
        clientX: 100,
        clientY: 100,
        pointerId,
      })
      expect(view.container.querySelector(
        '.pipeline-valve-installation-ghost--preview',
      )).not.toBeNull()
    }

    begin(4)
    fireEvent.keyDown(control, { key: 'Escape' })
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost',
    )).toBeNull()
    begin(5)
    fireEvent.pointerCancel(control, { pointerId: 5 })
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost',
    )).toBeNull()
    begin(6)
    fireEvent.blur(window)
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost',
    )).toBeNull()
    expect(onModeCommand).not.toHaveBeenCalled()
  })

  it('supports click and keyboard fallback once and keeps focus after rejection', async () => {
    const user = userEvent.setup()
    const onModeCommand = vi.fn()
    render(
      <Harness
        modeAccepted={false}
        onModeCommand={onModeCommand}
      />,
    )
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })

    fireEvent.keyDown(control, { key: 'Enter', repeat: true })
    expect(onModeCommand).not.toHaveBeenCalled()
    await user.click(control)
    await waitFor(() => {
      expect(onModeCommand).toHaveBeenCalledOnce()
      expect(document.activeElement).toBe(control)
      expect(screen.getByText('Valve mode change rejected')).not.toBeNull()
    })
  })

  it('renders backend pending mode as a yellow ghost without changing applied state', () => {
    const view = render(
      <Harness pendingInstallationMode="bypass" />,
    )
    const control = screen.getByRole('button', {
      name: 'Remove throttler valve',
    })

    expect(control.getAttribute('aria-pressed')).toBe('false')
    expect(control.getAttribute('aria-valuetext')).toContain('bypass pending')
    expect(view.container.querySelector(
      '.pipeline-valve-installation-ghost--pending',
    )).not.toBeNull()
    expect(view.container.querySelector(
      '.pipeline-valve-detached-assembly--applied',
    )).toBeNull()
  })

  it('keeps applied opening solid while preview takes precedence over pending', () => {
    const view = render(
      <Harness control={requestedControl({ pending: 135_000 })} />,
    )
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })

    expect(slider.getAttribute('aria-valuenow')).toBe('6')
    expect(screen.getByText('45% OPEN')).not.toBeNull()
    expect(view.container.querySelector('.pipeline-valve-ghost--pending'))
      .not.toBeNull()

    view.rerender(
      <Harness
        control={requestedControl({ preview: 160_000, pending: 135_000 })}
      />,
    )
    expect(slider.getAttribute('aria-valuenow')).toBe('7')
    expect(screen.getByText('45% OPEN')).not.toBeNull()
    expect(view.container.querySelector('.pipeline-valve-ghost--preview'))
      .not.toBeNull()
  })

  it('clears a rejected ghost and keeps invalid capabilities readonly', async () => {
    const user = userEvent.setup()
    const rejected = vi.fn()
    const view = render(
      <Harness
        control={requestedControl({ applied: 0 })}
        accepted={false}
        onCommand={rejected}
      />,
    )
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)
    await user.keyboard('{ArrowRight}')
    await waitFor(() => {
      expect(slider.getAttribute('aria-valuenow')).toBe('0')
      expect(view.container.querySelector('.pipeline-valve-ghost')).toBeNull()
    })

    view.rerender(
      <Harness
        control={requestedControl({ applied: 0, max: 10, step: 2 })}
        onCommand={rejected}
      />,
    )
    const readonly = screen.getByRole('slider', { name: 'Throttle opening' })
    expect(readonly.getAttribute('aria-disabled')).toBe('true')
    await user.click(readonly)
    await user.keyboard('{ArrowRight}{PageUp}{End}')
    expect(rejected).toHaveBeenCalledTimes(1)
  })

  it('repeats a held pointer step and stops after release', () => {
    vi.useFakeTimers()
    const onCommand = vi.fn()
    const view = render(
      <Harness
        control={requestedControl({ applied: 0 })}
        onCommand={onCommand}
      />,
    )
    const increase = view.container.querySelector('[data-direction="increase"]')!

    fireEvent.pointerDown(increase)
    act(() => vi.advanceTimersByTime(700))
    fireEvent.pointerUp(window)
    const callsAtRelease = onCommand.mock.calls.length

    expect(onCommand.mock.calls.map(([value]) => value))
      .toEqual([25_000, 45_000, 70_000])
    act(() => vi.advanceTimersByTime(1_000))
    expect(onCommand).toHaveBeenCalledTimes(callsAtRelease)
  })

  it('keeps outer geometry invariant through six distinct internal phases', async () => {
    const user = userEvent.setup()
    const view = render(
      <Harness control={requestedControl({ applied: 0 })} />,
    )
    const slider = screen.getByRole('slider', { name: 'Throttle opening' })
    await user.click(slider)
    const rimGeometry: string[] = []
    const visibleBounds: string[] = []
    const internalPhases: string[] = []
    const orbitLayouts: number[][] = []

    for (let phase = 0; phase < 6; phase += 1) {
      const rim = view.container.querySelector('.pipeline-valve-wheel-rim')!
      const body = view.container.querySelector('.pipeline-valve-body')!
      const inner = view.container.querySelector('.pipeline-valve-wheel-inner-motion')!
      rimGeometry.push([
        rim.getAttribute('cx'), rim.getAttribute('cy'),
        rim.getAttribute('rx'), rim.getAttribute('ry'),
      ].join(':'))
      visibleBounds.push(body.getAttribute('data-visible-bounds')!)
      internalPhases.push(inner.getAttribute('data-internal-phase')!)
      orbitLayouts.push([...view.container.querySelectorAll(
        '.pipeline-valve-wheel-knob',
      )].map((knob) => Number(knob.getAttribute('data-orbit-angle')))
        .sort((left, right) => left - right))
      expect([...view.container.querySelectorAll(
        '.pipeline-valve-wheel-knob',
      )].every((knob) => {
        const x = Number(knob.getAttribute('cx'))
        const y = Number(knob.getAttribute('cy'))
        const radius = Number(knob.getAttribute('r'))
        return x - radius >= 400.25 && x + radius <= 459.75 &&
          y - radius >= 336.75 && y + radius <= 363.25
      })).toBe(true)
      await user.keyboard('{ArrowRight}')
    }

    expect(new Set(rimGeometry)).toHaveLength(1)
    expect(new Set(visibleBounds)).toHaveLength(1)
    expect(internalPhases).toEqual(['0', '1', '2', '3', '4', '5'])
    expect(orbitLayouts).toEqual([
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
      [0, 60, 120, 180, 240, 300],
      [30, 90, 150, 210, 270, 330],
    ])
    const orderedDepths = [...view.container.querySelectorAll(
      '.pipeline-valve-wheel-knob',
    )].map((knob) => Number(knob.getAttribute('data-orbit-depth')))
    expect(orderedDepths).toEqual([...orderedDepths].sort((a, b) => a - b))
    expect([...view.container.querySelectorAll(
      '.pipeline-valve-wheel-knob',
    )].every((knob) => knob.getAttribute('data-orbit-layer') === (
      Number(knob.getAttribute('data-orbit-depth')) < 0 ? 'back' : 'front'
    ))).toBe(true)
  })

  it('shares perspective and clips closed partial open and ghost pistons', () => {
    const view = render(
      <Harness control={requestedControl({ applied: 0 })} />,
    )
    const readPose = () => Number(view.container.querySelector(
      '[data-gate-kind="applied"]',
    )!.getAttribute('data-gate-y'))
    const aperture = view.container.querySelector('.pipeline-valve-aperture')!
    const body = view.container.querySelector('.pipeline-valve-body')!
    const gate = view.container.querySelector('[data-gate-kind="applied"]')!
    const gateEllipse = gate.querySelector('ellipse')!
    const hits = [...view.container.querySelectorAll('.pipeline-valve-hit-area')]

    expect(aperture.getAttribute('data-axis-ratio')).toBe('0.82')
    expect(Number(gateEllipse.getAttribute('ry')) /
      Number(gateEllipse.getAttribute('rx'))).toBeCloseTo(0.82, 8)
    expect(gate.getAttribute('data-piston-axis-ratio')).toBe('0.82')
    expect(gate.getAttribute('clip-path'))
      .toBe('url(#throttler-valve-aperture-clip)')
    expect(gate.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING)
      .not.toBe(0)
    expect(readPose()).toBe(415)
    expect(body.getAttribute('data-visible-bounds'))
      .toBe('400.25 336.75 59.5 95.25')
    expect(hits.map((hit) => [
      hit.getAttribute('x'), hit.getAttribute('y'),
      hit.getAttribute('width'), hit.getAttribute('height'),
    ])).toEqual([
      ['382', '321', '48', '46'],
      ['430', '321', '48', '46'],
    ])

    view.rerender(<Harness control={requestedControl({ applied: 115_000 })} />)
    const partialY = readPose()
    expect(partialY).toBeGreaterThan(393)
    expect(partialY).toBeLessThan(415)
    expect(partialY + Number(gateEllipse.getAttribute('ry')))
      .toBeLessThan(415 + 12.3)

    view.rerender(<Harness control={requestedControl({ applied: 250_000 })} />)
    expect(readPose()).toBe(393)
    const openEdge = readPose() + Number(gateEllipse.getAttribute('ry')) -
      (415 - 12.3)
    expect(openEdge).toBeGreaterThan(0)
    expect(openEdge).toBeLessThan(1)
    expect([...view.container.querySelectorAll('[data-gate-kind]')].every(
      (item) => item.getAttribute('clip-path') ===
        'url(#throttler-valve-aperture-clip)',
    )).toBe(true)

    view.rerender(<Harness control={requestedControl({ pending: 135_000 })} />)
    const ghost = view.container.querySelector('[data-gate-kind="pending"]')!
    expect(ghost.getAttribute('data-piston-axis-ratio')).toBe('0.82')
    expect(Number(ghost.querySelector('ellipse')!.getAttribute('ry')) /
      Number(ghost.querySelector('ellipse')!.getAttribute('rx')))
      .toBeCloseTo(0.82, 8)
  })

  it('leaves an exact flange connector gap and colors it from applied flow only', () => {
    const view = render(
      <Harness
        control={requestedControl({ applied: 0, preview: 250_000, pending: 225_000 })}
        queueOverrides={{ flowState: 'normal', displayedPressure: 0 }}
      />,
    )
    const connectors = [...view.container.querySelectorAll(
      '.pipeline-valve-flow-line',
    )]
    expect(connectors.map((line) => [
      line.getAttribute('data-connector-side'),
      Number(line.getAttribute('x1')),
      Number(line.getAttribute('x2')),
    ])).toEqual([
      ['left', 355, 401],
      ['right', 459, 505],
    ])
    expect(connectors.every((line) =>
      Number(line.getAttribute('x2')) <= 401 ||
      Number(line.getAttribute('x1')) >= 459
    )).toBe(true)
    const actor = view.container.querySelector<SVGGElement>('#throttler-actor')!
    expect(actor.style.getPropertyValue('--pipeline-valve-flow-color'))
      .toBe('#79d957')

    view.rerender(
      <Harness
        control={requestedControl({ applied: 0, preview: 250_000, pending: 225_000 })}
        queueOverrides={{ flowState: 'backpressure', displayedPressure: 1 }}
      />,
    )
    expect(actor.style.getPropertyValue('--pipeline-valve-flow-color'))
      .toBe('#ff6748')
  })
})
