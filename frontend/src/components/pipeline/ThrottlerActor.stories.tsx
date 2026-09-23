import type { Meta, StoryObj } from '@storybook/react-vite'
import type { ComponentProps } from 'react'
import { ThrottlerActor } from './ThrottlerActor'
import './PipelineSvg.css'

type ThrottlerActorProps = ComponentProps<typeof ThrottlerActor>

const noop = () => {}
const accept = async () => true

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

function installationModeControl(
  applied: 'installed' | 'bypass',
  desired: 'installed' | 'bypass',
  phase: 'idle' | 'pending',
): ThrottlerActorProps['installationModeControl'] {
  return {
    applied,
    available: true,
    desired,
    error: null,
    phase,
    preview: noop,
    commit: accept,
    cancel: noop,
  } as ThrottlerActorProps['installationModeControl']
}

function createArgs({
  appliedTps,
  desiredTps = appliedTps,
  requestedPhase = 'idle',
  installationMode = 'installed',
}: {
  appliedTps: number
  desiredTps?: number
  requestedPhase?: 'idle' | 'preview'
  installationMode?: 'installed' | 'bypass'
}): ThrottlerActorProps {
  return {
    snapshot: {
      id: 'throttler',
      requestedTps: {
        applied: appliedTps,
        preview: null,
        pending: null,
        min: 0,
        max: 250_000,
        step: 5_000,
        unit: 'tx/s',
        applyMode: 'immediate',
      },
      installationMode: {
        applied: installationMode,
        pending: null,
      },
    } as ThrottlerActorProps['snapshot'],
    upstreamChannel: {
      flowState: 'normal',
      displayedPressure: 0,
    } as ThrottlerActorProps['upstreamChannel'],
    requestedTpsControl: requestedTpsControl(
      appliedTps,
      desiredTps,
      requestedPhase,
    ),
    installationModeControl: installationModeControl(
      installationMode,
      installationMode,
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
  args: createArgs({ appliedTps: 120_000, installationMode: 'bypass' }),
}

export const DesiredNotYetApplied: Story = {
  args: createArgs({
    appliedTps: 120_000,
    desiredTps: 160_000,
    requestedPhase: 'preview',
  }),
}
