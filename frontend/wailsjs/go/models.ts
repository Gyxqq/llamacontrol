export namespace main {
	
	export class DownloadRequest {
	    repoId: string;
	    filename: string;
	    revision: string;
	    displayName: string;
	    hfToken: string;
	
	    static createFrom(source: any = {}) {
	        return new DownloadRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repoId = source["repoId"];
	        this.filename = source["filename"];
	        this.revision = source["revision"];
	        this.displayName = source["displayName"];
	        this.hfToken = source["hfToken"];
	    }
	}
	export class HuggingFaceModel {
	    id: string;
	    downloads: number;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new HuggingFaceModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.downloads = source["downloads"];
	        this.description = source["description"];
	    }
	}
	export class LlamaReleaseAsset {
	    name: string;
	    size: number;
	    downloadUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new LlamaReleaseAsset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	        this.downloadUrl = source["downloadUrl"];
	    }
	}
	export class LlamaServerDownloadProgress {
	    downloading: boolean;
	    releaseTag: string;
	    assetName: string;
	    totalBytes: number;
	    downloadedBytes: number;
	    completed: boolean;
	    error: string;
	    found: boolean;
	    version: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new LlamaServerDownloadProgress(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.downloading = source["downloading"];
	        this.releaseTag = source["releaseTag"];
	        this.assetName = source["assetName"];
	        this.totalBytes = source["totalBytes"];
	        this.downloadedBytes = source["downloadedBytes"];
	        this.completed = source["completed"];
	        this.error = source["error"];
	        this.found = source["found"];
	        this.version = source["version"];
	        this.path = source["path"];
	    }
	}
	export class LlamaServerInfo {
	    found: boolean;
	    path: string;
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new LlamaServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.found = source["found"];
	        this.path = source["path"];
	        this.version = source["version"];
	    }
	}
	export class LlamaServerRelease {
	    tagName: string;
	    name: string;
	    publishedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new LlamaServerRelease(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tagName = source["tagName"];
	        this.name = source["name"];
	        this.publishedAt = source["publishedAt"];
	    }
	}
	export class ModelRecord {
	    id: string;
	    displayName: string;
	    repoId: string;
	    filename: string;
	    revision: string;
	    localPath: string;
	    sizeBytes: number;
	    downloadedBytes: number;
	    state: string;
	    error: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.repoId = source["repoId"];
	        this.filename = source["filename"];
	        this.revision = source["revision"];
	        this.localPath = source["localPath"];
	        this.sizeBytes = source["sizeBytes"];
	        this.downloadedBytes = source["downloadedBytes"];
	        this.state = source["state"];
	        this.error = source["error"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ServerConfig {
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
	
	    static createFrom(source: any = {}) {
	        return new ServerConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.modelId = source["modelId"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.ctxSize = source["ctxSize"];
	        this.gpuLayers = source["gpuLayers"];
	        this.threads = source["threads"];
	        this.batchSize = source["batchSize"];
	        this.ubatchSize = source["ubatchSize"];
	        this.parallel = source["parallel"];
	        this.flashAttention = source["flashAttention"];
	        this.background = source["background"];
	        this.extraArgs = source["extraArgs"];
	    }
	}
	export class ServerStatus {
	    running: boolean;
	    pid: number;
	    endpoint: string;
	    modelId: string;
	    modelName: string;
	    startedAt: string;
	    commandLine: string;
	    logTail: string[];
	
	    static createFrom(source: any = {}) {
	        return new ServerStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.pid = source["pid"];
	        this.endpoint = source["endpoint"];
	        this.modelId = source["modelId"];
	        this.modelName = source["modelName"];
	        this.startedAt = source["startedAt"];
	        this.commandLine = source["commandLine"];
	        this.logTail = source["logTail"];
	    }
	}

}

