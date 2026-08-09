import type {
  AdapterKind,
  CommandReceipt,
  LoadgenCommand,
  LoadgenTelemetrySnapshot,
} from '../model/loadgen'

export type LoadgenSnapshotListener = (
  snapshot: LoadgenTelemetrySnapshot,
) => void

export interface LoadgenAdapter {
  readonly kind: AdapterKind
  getSnapshot(): LoadgenTelemetrySnapshot
  subscribe(listener: LoadgenSnapshotListener): () => void
  dispatch(command: LoadgenCommand): Promise<CommandReceipt>
  dispose(): void
}
