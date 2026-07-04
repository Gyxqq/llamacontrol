export type ModelState =
  | "missing"
  | "downloading"
  | "ready"
  | "failed";

export interface ModelRecord {
  id: string;
  displayName: string;
  repoId: string;
  filename: string;
  revision?: string;

  localPath?: string;
  sizeBytes?: number;
  downloadedBytes?: number;
  downloadStartedAt?: string;
  downloadSpeedBytesPerSecond?: number;
  downloadElapsedSeconds?: number;
  downloadRemainingSeconds?: number;

  state: ModelState;
  error?: string;

  createdAt?: string;
  updatedAt?: string;
}

export interface DownloadRequest {
  repoId: string;
  filename: string;
  revision?: string;
  displayName?: string;
  hfToken?: string;
}

export interface ServerConfig {
  modelId: string;
  host: string;
  port: number;

  ctxSize: number;
  gpuLayers: number;
  threads: number;
  batchSize: number;
  ubatchSize: number;
  parallel: number;

  flashAttention: boolean;
  background: boolean;
  extraArgs: string;

  // Enabled flags — gate whether each parameter is passed to llama-server
  hostEnabled: boolean;
  portEnabled: boolean;
  ctxSizeEnabled: boolean;
  gpuLayersEnabled: boolean;
  threadsEnabled: boolean;
  batchSizeEnabled: boolean;
  ubatchSizeEnabled: boolean;
  parallelEnabled: boolean;
  flashAttentionEnabled: boolean;
  extraArgsEnabled: boolean;
}

export interface ServerStatus {
  running: boolean;
  pid?: number;
  endpoint?: string;

  modelId?: string;
  modelName?: string;
  startedAt?: string;

  commandLine?: string;
  logTail?: string[];
}

export interface HuggingFaceModel {
  id: string;
  downloads?: number;
  description?: string;
}

export interface LlamaServerInfo {
  found: boolean;
  path?: string;
  version?: string;
}

export interface LlamaServerDownloadProgress {
  downloading: boolean;
  releaseTag: string;
  assetName: string;
  totalBytes: number;
  downloadedBytes: number;
  downloadStartedAt?: string;
  downloadSpeedBytesPerSecond: number;
  downloadElapsedSeconds: number;
  downloadRemainingSeconds: number;
  completed: boolean;
  error?: string;
  found: boolean;
  version?: string;
  path?: string;
  // CUDA Runtime download progress
  cudaDownloading: boolean;
  cudaAssetName: string;
  cudaTotalBytes: number;
  cudaDownloadedBytes: number;
  cudaDownloadStartedAt?: string;
  cudaDownloadSpeedBytesPerSecond: number;
  cudaDownloadElapsedSeconds: number;
  cudaDownloadRemainingSeconds: number;
  cudaCompleted: boolean;
  cudaError?: string;
}

export interface LlamaServerRelease {
  tagName: string;
  name: string;
  publishedAt: string;
}

export interface LlamaReleaseAsset {
  name: string;
  size: number;
  downloadUrl: string;
}

export interface GguFileInfo {
  path: string;
  size: number;
}
