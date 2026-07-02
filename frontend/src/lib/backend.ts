import type {
  DownloadRequest,
  HuggingFaceModel,
  LlamaReleaseAsset,
  LlamaServerInfo,
  LlamaServerRelease,
  ModelRecord,
  ServerConfig,
  ServerStatus,
} from "../types";

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, (...args: unknown[]) => Promise<unknown>>;
      };
    };
    runtime?: {
      WindowMinimise: () => void;
      WindowMaximise: () => void;
      WindowUnmaximise: () => void;
      WindowToggleMaximise: () => void;
      WindowIsMaximised: () => Promise<boolean>;
      Quit: () => void;
    };
  }
}

function app() {
  return window.go?.main?.App;
}

export function hasBackend(): boolean {
  return Boolean(app());
}

async function call<T>(name: string, ...args: unknown[]): Promise<T> {
  const fn = app()?.[name];

  if (!fn) {
    throw new Error(`Wails backend method not found: ${name}`);
  }

  return (await fn(...args)) as T;
}

export const backend = {
  ListModels(): Promise<ModelRecord[]> {
    return call<ModelRecord[]>("ListModels");
  },

  StartModelDownload(req: DownloadRequest): Promise<void> {
    return call<void>("StartModelDownload", req);
  },

  CancelModelDownload(modelId: string): Promise<void> {
    return call<void>("CancelModelDownload", modelId);
  },

  DeleteModel(modelId: string): Promise<void> {
    return call<void>("DeleteModel", modelId);
  },

  GetServerStatus(): Promise<ServerStatus> {
    return call<ServerStatus>("GetServerStatus");
  },

  StartLlamaServer(config: ServerConfig): Promise<void> {
    return call<void>("StartLlamaServer", config);
  },

  StopLlamaServer(): Promise<void> {
    return call<void>("StopLlamaServer");
  },

  OpenModelsDir(): Promise<void> {
    return call<void>("OpenModelsDir");
  },

  SearchHuggingFaceModels(query: string): Promise<HuggingFaceModel[]> {
    return call<HuggingFaceModel[]>("SearchHuggingFaceModels", query);
  },

  ListModelGguFiles(repoId: string): Promise<string[]> {
    return call<string[]>("ListModelGguFiles", repoId);
  },

  GetLlamaServerInfo(): Promise<LlamaServerInfo> {
    return call<LlamaServerInfo>("GetLlamaServerInfo");
  },

  ListLlamaServerReleases(): Promise<LlamaServerRelease[]> {
    return call<LlamaServerRelease[]>("ListLlamaServerReleases");
  },

  ListLlamaReleaseAssets(releaseTag: string): Promise<LlamaReleaseAsset[]> {
    return call<LlamaReleaseAsset[]>("ListLlamaReleaseAssets", releaseTag);
  },

  DownloadLlamaServerRelease(releaseTag: string, assetName: string): Promise<void> {
    return call<void>("DownloadLlamaServerRelease", releaseTag, assetName);
  },
};