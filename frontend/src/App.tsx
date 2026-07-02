import { useEffect, useMemo, useState } from "react";
import "./App.css";

import type {
  DownloadRequest,
  HuggingFaceModel,
  LlamaServerInfo,
  LlamaServerRelease,
  ModelRecord,
  ServerConfig,
  ServerStatus,
} from "./types";
import { backend, hasBackend } from "./lib/backend";

const defaultDownload: DownloadRequest = {
  repoId: "",
  filename: "",
  revision: "main",
  displayName: "",
  hfToken: "",
};

const defaultServerConfig: ServerConfig = {
  modelId: "",
  host: "127.0.0.1",
  port: 8080,

  ctxSize: 4096,
  gpuLayers: 999,
  threads: 8,
  batchSize: 512,
  ubatchSize: 512,
  parallel: 1,

  flashAttention: true,
  background: true,
  extraArgs: "",
};

const emptyStatus: ServerStatus = {
  running: false,
  logTail: [],
};

function formatBytes(value?: number): string {
  if (!value || value <= 0) return "-";

  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let index = 0;

  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }

  return `${size.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function downloadPercent(model: ModelRecord): number {
  if (!model.sizeBytes || !model.downloadedBytes) return 0;
  return Math.max(
    0,
    Math.min(100, (model.downloadedBytes / model.sizeBytes) * 100),
  );
}

function statusText(state: ModelRecord["state"]): string {
  switch (state) {
    case "ready":
      return "已下载";
    case "downloading":
      return "下载中";
    case "failed":
      return "失败";
    case "missing":
      return "未下载";
    default:
      return state;
  }
}

function inferName(repoId: string, filename: string): string {
  const repoName = repoId.split("/").filter(Boolean).pop() ?? repoId;
  const fileName = filename.replace(/\.gguf$/i, "");
  return fileName ? `${repoName} / ${fileName}` : repoName;
}

function App() {
  const [models, setModels] = useState<ModelRecord[]>([]);
  const [selectedModelId, setSelectedModelId] = useState("");
  const [downloadForm, setDownloadForm] =
    useState<DownloadRequest>(defaultDownload);
  const [serverConfig, setServerConfig] =
    useState<ServerConfig>(defaultServerConfig);
  const [serverStatus, setServerStatus] =
    useState<ServerStatus>(emptyStatus);

  const [maximized, setMaximized] = useState(false);
  const [loading, setLoading] = useState(false);
  const [operation, setOperation] = useState("");
  const [error, setError] = useState("");

  const [searchQuery, setSearchQuery] = useState("");
  const [searchResults, setSearchResults] = useState<HuggingFaceModel[]>([]);
  const [searching, setSearching] = useState(false);
  const [ggufFiles, setGgufFiles] = useState<string[]>([]);
  const [loadingFiles, setLoadingFiles] = useState(false);

  const [llamaInfo, setLlamaInfo] = useState<LlamaServerInfo>({ found: false });
  const [llamaReleases, setLlamaReleases] = useState<LlamaServerRelease[]>([]);
  const [selectedRelease, setSelectedRelease] = useState("");
  const [downloadingLlama, setDownloadingLlama] = useState(false);

  const backendReady = hasBackend();

  function minimise() {
    window.runtime?.WindowMinimise();
  }

  function toggleMaximise() {
    window.runtime?.WindowToggleMaximise();
    setMaximized((prev) => !prev);
  }

  function closeWin() {
    window.runtime?.Quit();
  }

  const selectedModel = useMemo(() => {
    return models.find((model) => model.id === selectedModelId);
  }, [models, selectedModelId]);

  const readyModels = useMemo(() => {
    return models.filter((model) => model.state === "ready");
  }, [models]);

  const downloadingModels = useMemo(() => {
    return models.filter((model) => model.state === "downloading");
  }, [models]);

  async function refresh() {
    if (!backendReady) return;

    try {
      const [modelList, status] = await Promise.all([
        backend.ListModels(),
        backend.GetServerStatus(),
      ]);

      setModels(modelList ?? []);
      setServerStatus(status ?? emptyStatus);

      if (!selectedModelId) {
        const firstReady = modelList?.find((item) => item.state === "ready");
        if (firstReady) {
          setSelectedModelId(firstReady.id);
          setServerConfig((old) => ({
            ...old,
            modelId: firstReady.id,
          }));
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  useEffect(() => {
    void refresh();

    const timer = window.setInterval(() => {
      void refresh();
    }, 1000);

    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [backendReady, selectedModelId]);

  function updateDownloadForm<K extends keyof DownloadRequest>(
    key: K,
    value: DownloadRequest[K],
  ) {
    setDownloadForm((old) => ({
      ...old,
      [key]: value,
    }));
  }

  function updateServerConfig<K extends keyof ServerConfig>(
    key: K,
    value: ServerConfig[K],
  ) {
    setServerConfig((old) => ({
      ...old,
      [key]: value,
    }));
  }

  async function startDownload() {
    setError("");

    const repoId = downloadForm.repoId.trim();
    const filename = downloadForm.filename.trim();

    if (!repoId) {
      setError("请填写 Hugging Face repo，例如：TheBloke/Llama-2-7B-Chat-GGUF");
      return;
    }

    if (!filename.toLowerCase().endsWith(".gguf")) {
      setError("当前版本只建议下载 .gguf 文件");
      return;
    }

    const req: DownloadRequest = {
      repoId,
      filename,
      revision: downloadForm.revision?.trim() || "main",
      displayName:
        downloadForm.displayName?.trim() || inferName(repoId, filename),
      hfToken: downloadForm.hfToken?.trim() || undefined,
    };

    try {
      setLoading(true);
      setOperation("正在创建下载任务");
      await backend.StartModelDownload(req);
      setDownloadForm(defaultDownload);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
      setOperation("");
    }
  }

  async function cancelDownload(modelId: string) {
    try {
      setError("");
      setOperation("正在取消下载");
      await backend.CancelModelDownload(modelId);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setOperation("");
    }
  }

  async function deleteModel(modelId: string) {
    try {
      setError("");
      setOperation("正在删除模型");
      await backend.DeleteModel(modelId);

      if (selectedModelId === modelId) {
        setSelectedModelId("");
        setServerConfig((old) => ({
          ...old,
          modelId: "",
        }));
      }

      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setOperation("");
    }
  }

  async function startServer() {
    setError("");

    const modelId = selectedModelId || serverConfig.modelId;

    if (!modelId) {
      setError("请先选择一个已下载模型");
      return;
    }

    try {
      setLoading(true);
      setOperation("正在启动 llama-server");

      await backend.StartLlamaServer({
        ...serverConfig,
        modelId,
      });

      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
      setOperation("");
    }
  }

  async function stopServer() {
    try {
      setError("");
      setOperation("正在停止 llama-server");
      await backend.StopLlamaServer();
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setOperation("");
    }
  }

  async function openModelsDir() {
    try {
      setError("");
      await backend.OpenModelsDir();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function searchModels() {
    const query = searchQuery.trim();
    if (!query) {
      setError("请输入关键词搜索 Hugging Face 模型");
      return;
    }
    setSearching(true);
    setError("");
    setSearchResults([]);
    try {
      const results = await backend.SearchHuggingFaceModels(query);
      setSearchResults(results ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setSearchResults([]);
    } finally {
      setSearching(false);
    }
  }

  async function handleDownloadLlama() {
    if (!selectedRelease) return;
    setDownloadingLlama(true);
    setError("");
    try {
      await backend.DownloadLlamaServerRelease(selectedRelease);
      const info = await backend.GetLlamaServerInfo();
      setLlamaInfo(info);
      if (info.found) {
        setSelectedRelease("");
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setDownloadingLlama(false);
    }
  }

  // Auto-fetch GGUF files when a repo is selected
  useEffect(() => {
    if (!downloadForm.repoId) {
      setGgufFiles([]);
      return;
    }

    let cancelled = false;

    async function fetchFiles() {
      setLoadingFiles(true);
      try {
        const files = await backend.ListModelGguFiles(downloadForm.repoId);
        if (!cancelled) {
          setGgufFiles(files ?? []);
          // Auto-select if only one GGUF file
          if (files?.length === 1) {
            updateDownloadForm("filename", files[0]);
          }
        }
      } catch (err) {
        if (!cancelled) {
          setGgufFiles([]);
        }
      } finally {
        if (!cancelled) {
          setLoadingFiles(false);
        }
      }
    }

    fetchFiles();
    return () => {
      cancelled = true;
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [downloadForm.repoId]);

  // Detect llama-server on startup; if not found, list available releases
  useEffect(() => {
    if (!backendReady) return;

    async function checkLlamaServer() {
      try {
        const info = await backend.GetLlamaServerInfo();
        setLlamaInfo(info);
        if (!info.found) {
          const releases = await backend.ListLlamaServerReleases();
          setLlamaReleases(releases ?? []);
        }
      } catch {
        // Silently ignore — llama.cpp detection is non-critical
      }
    }

    checkLlamaServer();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [backendReady]);

  return (
    <main className="app">
      <div className="titlebar">
        <span className="titlebarTitle">llamacontrol</span>
        <div className="titlebarControls">
          <button
            className="winBtn minimizeBtn"
            onClick={minimise}
            title="最小化"
          >
            <svg width="12" height="12" viewBox="0 0 12 12">
              <rect x="1" y="5.5" width="10" height="1" fill="currentColor" />
            </svg>
          </button>
          <button
            className="winBtn maximizeBtn"
            onClick={toggleMaximise}
            title="最大化"
          >
            {maximized ? (
              <svg width="12" height="12" viewBox="0 0 12 12">
                <rect x="3" y="0.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" strokeWidth="1" />
                <rect x="0.5" y="3" width="8" height="8" rx="1" fill="currentColor" />
              </svg>
            ) : (
              <svg width="12" height="12" viewBox="0 0 12 12">
                <rect x="1.5" y="1.5" width="9" height="9" rx="1" fill="none" stroke="currentColor" strokeWidth="1" />
              </svg>
            )}
          </button>
          <button
            className="winBtn closeBtn"
            onClick={closeWin}
            title="关闭"
          >
            <svg width="12" height="12" viewBox="0 0 12 12">
              <path d="M1 1l10 10M11 1L1 11" stroke="currentColor" strokeWidth="1.2" />
            </svg>
          </button>
        </div>
      </div>

      <div className="appContent">
        <header className="topbar">
          <div>
            <h1>llama control</h1>
            <p>本地 GGUF 模型管理器 · llama.cpp / llama-server</p>
          </div>

          <div className="topbarActions">
            <button className="ghost" onClick={refresh} disabled={!backendReady}>
              刷新
            </button>
            <button
              className="ghost"
              onClick={openModelsDir}
              disabled={!backendReady}
            >
              打开模型目录
            </button>
          </div>
        </header>

        {!backendReady && (
          <section className="notice warning">
            当前没有检测到 Wails 后端绑定。页面可以编译，但按钮需要 Go 后端实现后才能工作。
          </section>
        )}

        {operation && <section className="notice">{operation}</section>}

        {error && (
          <section className="notice error">
            <span>{error}</span>
            <button onClick={() => setError("")}>关闭</button>
          </section>
        )}

        <section className="layout">
        <section className="panel">
          <div className="panelHeader">
            <div>
              <h2>下载模型</h2>
              <p>从 Hugging Face 下载 GGUF 文件</p>
            </div>
          </div>

          <label className="field">
            <span>搜索模型</span>
            <div className="searchRow">
              <input
                placeholder="输入关键词搜索 Hugging Face 模型库..."
                value={searchQuery}
                onChange={(event) => setSearchQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void searchModels();
                }}
              />
              <button
                className="primary searchBtn"
                onClick={() => void searchModels()}
                disabled={searching}
              >
                {searching ? "搜索中..." : "搜索"}
              </button>
            </div>

            {searchResults.length > 0 && (
              <div className="searchResults">
                {searchResults.map((model) => (
                  <div
                    key={model.id}
                    className={`searchResult ${downloadForm.repoId === model.id ? "active" : ""}`}
                    onClick={() => {
                      updateDownloadForm("repoId", model.id);
                      updateDownloadForm("filename", "");
                      setSearchResults([]);
                    }}
                  >
                    <span className="searchResultId">{model.id}</span>
                    {model.downloads != null && model.downloads > 0 && (
                      <span className="searchResultDownloads">
                        {model.downloads.toLocaleString()} 下载
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}

            {downloadForm.repoId && (
              <div className="selectedRepo">
                <span>已选: {downloadForm.repoId}</span>
                <button
                  className="ghost"
                  onClick={() => {
                    updateDownloadForm("repoId", "");
                    updateDownloadForm("filename", "");
                    setGgufFiles([]);
                  }}
                >
                  清除
                </button>
              </div>
            )}
          </label>

          <label className="field">
            <span>选择 GGUF 文件</span>
            <select
              value={downloadForm.filename}
              onChange={(event) =>
                updateDownloadForm("filename", event.target.value)
              }
              disabled={!downloadForm.repoId || loadingFiles}
            >
              <option value="">
                {loadingFiles
                  ? "加载中..."
                  : ggufFiles.length > 0
                    ? "请选择 GGUF 文件"
                    : downloadForm.repoId
                      ? "该仓库没有 GGUF 文件"
                      : "请先搜索并选择模型仓库"}
              </option>
              {ggufFiles.map((file) => (
                <option key={file} value={file}>
                  {file}
                </option>
              ))}
            </select>
          </label>

          <div className="twoColumns">
            <label className="field">
              <span>Revision</span>
              <input
                placeholder="main"
                value={downloadForm.revision}
                onChange={(event) =>
                  updateDownloadForm("revision", event.target.value)
                }
              />
            </label>

            <label className="field">
              <span>显示名称</span>
              <input
                placeholder="留空则自动生成"
                value={downloadForm.displayName}
                onChange={(event) =>
                  updateDownloadForm("displayName", event.target.value)
                }
              />
            </label>
          </div>

          <label className="field">
            <span>HF Token</span>
            <input
              type="password"
              placeholder="私有模型才需要"
              value={downloadForm.hfToken}
              onChange={(event) =>
                updateDownloadForm("hfToken", event.target.value)
              }
            />
          </label>

          <button
            className="primary full"
            onClick={startDownload}
            disabled={loading || !backendReady}
          >
            开始下载
          </button>

          {downloadingModels.map((model) => (
            <div key={model.id} className="downloadProgress">
              <div className="downloadProgressHeader">
                <span className="downloadProgressName" title={model.displayName}>
                  {model.displayName}
                </span>
                <span className="downloadProgressPct">
                  {downloadPercent(model).toFixed(1)}%
                </span>
              </div>
              <div className="progress">
                <div
                  style={{ width: `${downloadPercent(model)}%` }}
                />
              </div>
              <div className="downloadProgressMeta">
                <span>
                  {formatBytes(model.downloadedBytes)} /{" "}
                  {formatBytes(model.sizeBytes)}
                </span>
                <button
                  className="ghost dangerText"
                  onClick={() => void cancelDownload(model.id)}
                >
                  取消
                </button>
              </div>
            </div>
          ))}

          <div className="llamaSection">
            <div className="panelHeader">
              <h2>llama.cpp</h2>
              <span className={`badge ${llamaInfo.found ? "ready" : "missing"}`}>
                {llamaInfo.found ? "已安装" : "未安装"}
              </span>
            </div>

            {llamaInfo.found ? (
              <p className="hint">
                llama-server 已就绪
                {llamaInfo.version && <> (版本: {llamaInfo.version})</>}
                {llamaInfo.path && (
                  <span className="metaPath"> · {llamaInfo.path}</span>
                )}
              </p>
            ) : (
              <>
                <p className="hint">
                  未检测到 llama-server，请选择版本下载安装
                </p>
                <label className="field">
                  <span>选择版本</span>
                  <select
                    value={selectedRelease}
                    onChange={(event) =>
                      setSelectedRelease(event.target.value)
                    }
                    disabled={downloadingLlama}
                  >
                    <option value="">
                      {llamaReleases.length > 0
                        ? "请选择版本"
                        : "加载中..."}
                    </option>
                    {llamaReleases.map((r) => (
                      <option key={r.tagName} value={r.tagName}>
                        {r.tagName}
                        {r.name ? ` — ${r.name}` : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <button
                  className="primary full"
                  onClick={() => void handleDownloadLlama()}
                  disabled={!selectedRelease || downloadingLlama}
                >
                  {downloadingLlama ? "下载中..." : "下载并安装"}
                </button>
              </>
            )}
          </div>
        </section>

        <section className="panel modelPanel">
          <div className="panelHeader">
            <div>
              <h2>本地模型</h2>
              <p>选择一个模型用于启动 llama-server</p>
            </div>

            <span className="count">{models.length}</span>
          </div>

          <label className="field">
            <span>选择模型</span>
            <select
              value={selectedModelId}
              onChange={(event) => {
                const id = event.target.value;
                setSelectedModelId(id);
                setServerConfig((old) => ({
                  ...old,
                  modelId: id,
                }));
              }}
            >
              <option value="">请选择模型</option>
              {models.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.displayName} — {statusText(model.state)}
                  {model.sizeBytes ? ` (${formatBytes(model.sizeBytes)})` : ""}
                </option>
              ))}
            </select>
          </label>

          {selectedModel && (
            <div className="modelDetail">
              <div className="modelMeta">
                <div>
                  <span className="metaLabel">Repo</span>
                  <span>{selectedModel.repoId}</span>
                </div>
                <div>
                  <span className="metaLabel">文件</span>
                  <span>{selectedModel.filename}</span>
                </div>
                <div>
                  <span className="metaLabel">大小</span>
                  <span>{formatBytes(selectedModel.sizeBytes)}</span>
                </div>
                {selectedModel.localPath && (
                  <div>
                    <span className="metaLabel">路径</span>
                    <span className="metaPath">{selectedModel.localPath}</span>
                  </div>
                )}
              </div>

              <span className={`badge ${selectedModel.state}`}>
                {statusText(selectedModel.state)}
              </span>

              {selectedModel.state === "downloading" && (
                <div className="progressBlock">
                  <div className="progressText">
                    <span>
                      {formatBytes(selectedModel.downloadedBytes)} /{" "}
                      {formatBytes(selectedModel.sizeBytes)}
                    </span>
                    <span>{downloadPercent(selectedModel).toFixed(1)}%</span>
                  </div>
                  <div className="progress">
                    <div
                      style={{ width: `${downloadPercent(selectedModel)}%` }}
                    />
                  </div>
                  <div className="modelActions">
                    <button
                      className="ghost dangerText"
                      onClick={() => void cancelDownload(selectedModel.id)}
                    >
                      取消
                    </button>
                  </div>
                </div>
              )}

              {selectedModel.error && (
                <pre className="inlineError">{selectedModel.error}</pre>
              )}

              {selectedModel.state !== "downloading" &&
                selectedModel.state !== "missing" && (
                  <div className="modelActions">
                    <button
                      className="ghost dangerText"
                      onClick={() => void deleteModel(selectedModel.id)}
                    >
                      删除
                    </button>
                  </div>
                )}
            </div>
          )}

          {models.length === 0 && (
            <div className="empty">
              还没有模型。先填入 Hugging Face repo 和 GGUF 文件名下载。
            </div>
          )}

          <section className="modelLog">
            <div className="modelLogHeader">
              <h3>服务日志</h3>
              <span>最近输出</span>
            </div>

            <pre className="logBox">
              {(serverStatus.logTail && serverStatus.logTail.length > 0
                ? serverStatus.logTail
                : ["暂无日志"]
              ).join("\n")}
            </pre>
          </section>
        </section>

        <section className="panel serverPanel">
          <div className="panelHeader">
            <div>
              <h2>运行服务</h2>
              <p>当前只支持 llama-server</p>
            </div>

            <span
              className={`serverDot ${
                serverStatus.running ? "running" : ""
              }`}
            />
          </div>

          <label className="field serverModelField">
            <span>模型</span>
            <select
              value={selectedModelId}
              onChange={(event) => {
                setSelectedModelId(event.target.value);
                updateServerConfig("modelId", event.target.value);
              }}
            >
              <option value="">请选择模型</option>
              {readyModels.map((model) => (
                <option key={model.id} value={model.id}>
                  {model.displayName}
                </option>
              ))}
            </select>
          </label>

          <div className="serverAddressGrid">
            <label className="field">
              <span>Host</span>
              <input
                value={serverConfig.host}
                onChange={(event) =>
                  updateServerConfig("host", event.target.value)
                }
              />
            </label>

            <label className="field">
              <span>Port</span>
              <input
                type="number"
                value={serverConfig.port}
                onChange={(event) =>
                  updateServerConfig("port", Number(event.target.value))
                }
              />
            </label>
          </div>

          <div className="serverParamGrid">
            <label className="field">
              <span>上下文</span>
              <input
                type="number"
                value={serverConfig.ctxSize}
                onChange={(event) =>
                  updateServerConfig("ctxSize", Number(event.target.value))
                }
              />
            </label>

            <label className="field">
              <span>GPU 层</span>
              <input
                type="number"
                value={serverConfig.gpuLayers}
                onChange={(event) =>
                  updateServerConfig("gpuLayers", Number(event.target.value))
                }
              />
            </label>

            <label className="field">
              <span>线程</span>
              <input
                type="number"
                value={serverConfig.threads}
                onChange={(event) =>
                  updateServerConfig("threads", Number(event.target.value))
                }
              />
            </label>

            <label className="field">
              <span>并行</span>
              <input
                type="number"
                value={serverConfig.parallel}
                onChange={(event) =>
                  updateServerConfig("parallel", Number(event.target.value))
                }
              />
            </label>

            <label className="field">
              <span>Batch</span>
              <input
                type="number"
                value={serverConfig.batchSize}
                onChange={(event) =>
                  updateServerConfig("batchSize", Number(event.target.value))
                }
              />
            </label>

            <label className="field">
              <span>uBatch</span>
              <input
                type="number"
                value={serverConfig.ubatchSize}
                onChange={(event) =>
                  updateServerConfig("ubatchSize", Number(event.target.value))
                }
              />
            </label>
          </div>

          <label className="field extraArgsField">
            <span>额外参数</span>
            <input
              placeholder="例如：--verbose --cache-type-k q8_0"
              value={serverConfig.extraArgs}
              onChange={(event) =>
                updateServerConfig("extraArgs", event.target.value)
              }
            />
          </label>

          <div className="serverControlRow">
            <div className="switchRow">
              <label>
                <input
                  type="checkbox"
                  checked={serverConfig.flashAttention}
                  onChange={(event) =>
                    updateServerConfig("flashAttention", event.target.checked)
                  }
                />
                Flash Attention
              </label>

              <label>
                <input
                  type="checkbox"
                  checked={serverConfig.background}
                  onChange={(event) =>
                    updateServerConfig("background", event.target.checked)
                  }
                />
                后台
              </label>
            </div>

            <div className="serverActions">
              <button
                className="primary"
                onClick={startServer}
                disabled={
                  loading ||
                  !backendReady ||
                  serverStatus.running ||
                  !selectedModel
                }
              >
                启动
              </button>

              <button
                className="secondary"
                onClick={stopServer}
                disabled={!backendReady || !serverStatus.running}
              >
                停止
              </button>
            </div>
          </div>

          <section className="statusBox compactStatus">
            <div className="statusLine">
              <span>状态</span>
              <strong>{serverStatus.running ? "运行中" : "未运行"}</strong>
            </div>

            {serverStatus.pid && (
              <div className="statusLine">
                <span>PID</span>
                <strong>{serverStatus.pid}</strong>
              </div>
            )}

            {serverStatus.endpoint && (
              <div className="statusLine">
                <span>Endpoint</span>
                <strong title={serverStatus.endpoint}>{serverStatus.endpoint}</strong>
              </div>
            )}

            {serverStatus.modelName && (
              <div className="statusLine">
                <span>模型</span>
                <strong title={serverStatus.modelName}>{serverStatus.modelName}</strong>
              </div>
            )}
          </section>
        </section>
        </section>
      </div>
    </main>
  );
}

export default App;
