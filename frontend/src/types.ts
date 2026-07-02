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

export interface LlamaServerRelease {
  tagName: string;
  name: string;
  publishedAt: string;
}