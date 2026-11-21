import type {
  AdapterKind,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
} from '../model/loadgen'
import type { WireConfig } from '../model/wireSnapshot'

export type LoadgenSnapshotListener = (snapshot: LoadgenTelemetrySnapshot) => void

export interface LoadgenAdapter {
  readonly kind: AdapterKind
  getSnapshot(): LoadgenTelemetrySnapshot
  getConfig(): Readonly<WireConfig> | null
  subscribe(listener: LoadgenSnapshotListener): () => void
  dispatch(command: LoadgenCommand): Promise<CommandReceipt>
  dispose(): void
}
