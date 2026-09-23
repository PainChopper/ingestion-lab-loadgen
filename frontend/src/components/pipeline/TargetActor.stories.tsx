import type { Meta, StoryObj } from '@storybook/react-vite'
import type { HttpSnapshot, TargetSnapshot } from '../../model/loadgen'
import { createPipelineGeometry } from './geometry'
import { HttpLink } from './HttpLink'
import { TargetActor } from './TargetActor'
import './PipelineSvg.css'

const geometry = createPipelineGeometry({
  orientation: 'landscape',
  readerWorkers: 0,
  senderWorkers: 0,
})

const noop = () => {}

type TargetHttpFixture = {
  target: TargetSnapshot
  http: HttpSnapshot
}

function TargetHttpStory({ target, http }: TargetHttpFixture) {
  return (
    <svg
      className="pipeline-svg pipeline-svg--landscape"
      viewBox={geometry.viewBox.value}
      preserveAspectRatio="xMinYMin meet"
      role="img"
      aria-label="Target and HTTP outcome fixture"
    >
      <rect width={geometry.viewBox.width} height={geometry.viewBox.height} fill="#101826" />
      <HttpLink
        snapshot={http}
        telemetryAvailable
        selected={false}
        onSelect={noop}
        geometry={geometry}
      />
      <TargetActor
        snapshot={target}
        telemetryAvailable
        selected={false}
        onSelect={noop}
        geometry={geometry.actors.target}
      />
    </svg>
  )
}

const meta = {
  title: 'Pipeline/Target HTTP',
  component: TargetHttpStory,
} satisfies Meta<typeof TargetHttpStory>

export default meta
type Story = StoryObj<typeof meta>

const target = (overrides: Partial<TargetSnapshot>): TargetSnapshot => ({
  id: 'target',
  endpoint: 'fixture://target',
  acceptedTps: 1_200,
  rejectedTps: 0,
  latencyP95Ms: 42,
  http200Responses: 11_900,
  http503Responses: 0,
  connectionState: 'connected',
  ...overrides,
})

const http = (overrides: Partial<HttpSnapshot>): HttpSnapshot => ({
  id: 'http',
  connectionState: 'connected',
  statusCode: 200,
  lastOutcome: 'http-response',
  throughputTps: 1_200,
  inFlightRequests: 1,
  requestsStartedTotal: 12_000,
  requestsCompletedTotal: 11_900,
  requestsSucceededTotal: 11_900,
  requestsFailedTotal: 0,
  requestsTimedOutTotal: 0,
  networkErrorsTotal: 0,
  latencyP95Ms: 42,
  ...overrides,
})

export const Accepted: Story = {
  args: {
    target: target({}),
    http: http({}),
  },
}

export const Rejected: Story = {
  args: {
    target: target({ acceptedTps: 0, rejectedTps: 40, http200Responses: 0, http503Responses: 40 }),
    http: http({ statusCode: 503, throughputTps: 0, inFlightRequests: 0, requestsCompletedTotal: 40, requestsSucceededTotal: 0, requestsFailedTotal: 40 }),
  },
}

export const Disconnected: Story = {
  args: {
    target: target({ endpoint: null, acceptedTps: null, rejectedTps: null, latencyP95Ms: null, http200Responses: null, http503Responses: null, connectionState: 'disconnected' }),
    http: http({ connectionState: 'disconnected', statusCode: null, lastOutcome: null, throughputTps: 0, inFlightRequests: 0, requestsStartedTotal: 0, requestsCompletedTotal: 0, requestsSucceededTotal: 0, requestsFailedTotal: 0, latencyP95Ms: null }),
  },
}

export const Timeout: Story = {
  args: {
    target: target({ acceptedTps: null, rejectedTps: null, latencyP95Ms: null, http200Responses: null }),
    http: http({ statusCode: null, lastOutcome: 'timeout', throughputTps: 0, inFlightRequests: 1, requestsCompletedTotal: 0, requestsSucceededTotal: 0, requestsTimedOutTotal: 1, latencyP95Ms: null }),
  },
}

export const TimeoutOrTerminalFailure: Story = {
  args: {
    target: target({ acceptedTps: 0, rejectedTps: 40, http200Responses: 0, http503Responses: 40 }),
    http: http({ statusCode: 503, throughputTps: 0, inFlightRequests: 0, requestsCompletedTotal: 40, requestsSucceededTotal: 0, requestsFailedTotal: 40 }),
  },
}
