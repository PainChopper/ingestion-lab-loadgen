import type { Meta, StoryObj } from '@storybook/react-vite'
import {
  useCallback,
  useRef,
  useState,
} from 'react'
import type { ComponentProps } from 'react'
import type { ChannelSnapshot, NumericControlSnapshot, ThrottlerSnapshot } from '../../model/loadgen'
import { ThrottlerActor } from './ThrottlerActor'
import './PipelineSvg.css'

type ThrottlerActorProps = ComponentProps<typeof ThrottlerActor>

const noop = () => {}
const accept = async () => true

function numericControl(applied: number, unit: string, step = 1): NumericControlSnapshot {
  return {
    applied,
    preview: null,
    pending: null,
    min: 0,
    max: 250_000,
    step,
    unit,
    applyMode: 'immediate',
  }
}

function requestedTpsControl(
  applied: number,
  desired: number,
  phase: 'idle' | 'preview',
): ThrottlerActorProps['requestedTpsControl'] {
  return {
    applied,
    available: true,
    desired,
    error: null,
    phase,
    preview: noop,
    commit: accept,
    cancel: noop,
  } as ThrottlerActorProps['requestedTpsControl']
}

function installedControl(
  applied: boolean,
  desired: boolean,
  phase: 'idle' | 'pending',
): ThrottlerActorProps['installedControl'] {
  return {
    applied,
    available: true,
    desired,
    error: null,
    phase,
    preview: noop,
    commit: accept,
    cancel: noop,
  } as ThrottlerActorProps['installedControl']
}

function createArgs({
  appliedTps,
  desiredTps = appliedTps,
  requestedPhase = 'idle',
  installed = true,
  admittedTps = appliedTps,
  flowState = 'normal',
  displayedPressure = 0,
}: {
  appliedTps: number
  desiredTps?: number
  requestedPhase?: 'idle' | 'preview'
  installed?: boolean
  admittedTps?: number
  flowState?: 'normal' | 'stopped' | 'connection-error'
  displayedPressure?: number
}): ThrottlerActorProps {
  const snapshot = {
    id: 'throttler',
    requestedTps: numericControl(appliedTps, 'tx/s', 5_000),
    installed: {
      applied: installed,
      pending: null,
      applyMode: 'immediate',
      writable: true,
      unavailableReason: null,
    },
    admittedTps,
    limitedMs: null,
    state: 'running',
  } satisfies ThrottlerSnapshot
  const upstreamChannel = {
    id: 'reader-to-throttler',
    from: 'reader',
    to: 'throttler',
    capacity: numericControl(32, 'batches'),
    sentBatchesTotal: 120,
    sentTransactionsTotal: 12_000,
    receivedBatchesTotal: 118,
    receivedTransactionsTotal: 11_800,
    depthBatches: 2,
    bufferedTransactions: 200,
    handoffBatches: 0,
    handoffBatchesTotal: 0,
    blockedSenders: 0,
    oldestBlockedSenderMs: 0,
    inputBatchesPerSecond: 12,
    outputBatchesPerSecond: 12,
    inputTransactionsPerSecond: 1_200,
    outputTransactionsPerSecond: 1_200,
    inputTps: 1_200,
    outputTps: 1_200,
    throughputTps: 1_200,
    blockedMs: null,
    trend: 'steady',
    flowState,
    displayedPressure,
  } satisfies ChannelSnapshot

  return {
    snapshot,
    upstreamChannel,
    requestedTpsControl: requestedTpsControl(
      appliedTps,
      desiredTps,
      requestedPhase,
    ),
    installedControl: installedControl(
      installed,
      installed,
      'idle',
    ),
    selected: false,
    onSelect: noop,
    geometry: {
      transform: { x: 0, y: 0 },
      renderTitle: { x: 430, y: 300, anchor: 'middle' },
      metrics: {
        installed: {
          requested: {
            caption: { x: 430, y: 490, anchor: 'middle' },
            value: { x: 430, y: 507, anchor: 'middle' },
          },
          admitted: {
            caption: { x: 430, y: 526, anchor: 'middle' },
            value: { x: 430, y: 543, anchor: 'middle' },
          },
        },
        bypass: {
          admitted: {
            caption: { x: 430, y: 490, anchor: 'middle' },
            value: { x: 430, y: 507, anchor: 'middle' },
          },
        },
      },
    } as ThrottlerActorProps['geometry'],
    orientation: 'landscape',
  }
}

function ThrottlerStory(args: ThrottlerActorProps) {
  return (
    <svg viewBox="0 0 1120 650" role="img" aria-label="Throttler fixture">
      <rect width="1120" height="650" fill="#101826" />
      <ThrottlerActor {...args} />
    </svg>
  )
}

type InteractiveControls = {
  requestedTps: number
  installed: boolean
  admittedTps: number
  displayedPressure: number
  flowState: 'normal' | 'stopped' | 'connection-error'
}

type InteractiveControlPhase = 'idle' | 'preview'
type InteractiveRequestedTps = {
  applied: number
  desired: number
  phase: InteractiveControlPhase
}
type InteractiveInstalled = {
  applied: boolean
  desired: boolean
  phase: InteractiveControlPhase
}

function InteractiveThrottler({ controls }: { controls: InteractiveControls }) {
  const [requestedTps, setRequestedTps] = useState<InteractiveRequestedTps>(() => ({
    applied: controls.requestedTps,
    desired: controls.requestedTps,
    phase: 'idle' as const,
  }))
  const [installed, setInstalled] = useState<InteractiveInstalled>(() => ({
    applied: controls.installed,
    desired: controls.installed,
    phase: 'idle' as const,
  }))
  const requestedTpsRef = useRef(requestedTps.desired)
  const installedRef = useRef(installed.desired)
  const [selected, setSelected] = useState(false)

  const previewRequestedTps = useCallback((value: number) => {
    requestedTpsRef.current = value
    setRequestedTps((current) => ({ ...current, desired: value, phase: 'preview' }))
  }, [])
  const commitRequestedTps = useCallback(async () => {
    const value = requestedTpsRef.current
    setRequestedTps({ applied: value, desired: value, phase: 'idle' })
    return true
  }, [])
  const cancelRequestedTps = useCallback(() => {
    setRequestedTps((current) => ({
      applied: current.applied,
      desired: current.applied,
      phase: 'idle',
    }))
    requestedTpsRef.current = requestedTps.applied
  }, [requestedTps.applied])

  const previewInstalled = useCallback((value: boolean) => {
    installedRef.current = value
    setInstalled((current) => ({ ...current, desired: value, phase: 'preview' }))
  }, [])
  const commitInstalled = useCallback(async () => {
    const value = installedRef.current
    setInstalled({ applied: value, desired: value, phase: 'idle' })
    return true
  }, [])
  const cancelInstalled = useCallback(() => {
    setInstalled((current) => ({
      applied: current.applied,
      desired: current.applied,
      phase: 'idle',
    }))
    installedRef.current = installed.applied
  }, [installed.applied])

  const args = createArgs({
    appliedTps: requestedTps.applied,
    desiredTps: requestedTps.desired,
    requestedPhase: requestedTps.phase,
    installed: installed.applied,
    admittedTps: controls.admittedTps,
    flowState: controls.flowState,
    displayedPressure: controls.displayedPressure,
  })
  args.selected = selected
  args.onSelect = () => setSelected((current) => !current)
  args.requestedTpsControl = {
    ...args.requestedTpsControl,
    applied: requestedTps.applied,
    desired: requestedTps.desired,
    phase: requestedTps.phase,
    preview: previewRequestedTps,
    commit: commitRequestedTps,
    cancel: cancelRequestedTps,
  }
  args.installedControl = {
    ...args.installedControl,
    applied: installed.applied,
    desired: installed.desired,
    phase: installed.phase,
    preview: previewInstalled,
    commit: commitInstalled,
    cancel: cancelInstalled,
  }

  return <ThrottlerStory {...args} />
}

const meta = {
  title: 'Pipeline/Throttler',
  component: ThrottlerStory,
} satisfies Meta<typeof ThrottlerStory>

export default meta
type Story = StoryObj<typeof meta>

export const InstalledClosed: Story = {
  args: createArgs({ appliedTps: 0 }),
}

export const InstalledOpen: Story = {
  args: createArgs({ appliedTps: 250_000 }),
}

export const InstalledIntermediate: Story = {
  args: createArgs({ appliedTps: 120_000 }),
}

export const BypassApplied: Story = {
  args: createArgs({ appliedTps: 120_000, installed: false }),
}

export const DesiredNotYetApplied: Story = {
  args: createArgs({
    appliedTps: 120_000,
    desiredTps: 160_000,
    requestedPhase: 'preview',
  }),
}

export const Interactive: StoryObj<InteractiveControls> = {
  args: {
    requestedTps: 120_000,
    installed: true,
    admittedTps: 120_000,
    displayedPressure: 0.35,
    flowState: 'normal',
  },
  argTypes: {
    requestedTps: {
      control: { type: 'number', min: 0, max: 250_000, step: 5_000 },
      description: 'Локально применяемый requested TPS и положение крана',
    },
    installed: {
      control: 'boolean',
    },
    admittedTps: {
      control: { type: 'number', min: 0, max: 250_000, step: 5_000 },
    },
    displayedPressure: {
      control: { type: 'range', min: 0, max: 1, step: 0.05 },
    },
    flowState: {
      control: 'select',
      options: ['normal', 'stopped', 'connection-error'],
    },
  },
  render: (controls) => (
    <InteractiveThrottler
      key={`${controls.requestedTps}-${controls.installed}-${controls.admittedTps}-${controls.displayedPressure}-${controls.flowState}`}
      controls={controls}
    />
  ),
}
