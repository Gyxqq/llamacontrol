# Backend Implementation TODO

- [x] Design backend state and persistence
  - [x] Decide model storage directory
  - [x] Decide metadata file location and schema
  - [x] Define in-memory download task state
  - [x] Define in-memory llama-server process state

- [x] Implement model metadata and `ListModels`
  - [x] Create helpers for loading and saving model metadata
  - [x] Create helpers for model IDs and safe local filenames
  - [x] Ensure the model directory exists at startup or first use
  - [x] Validate local files still exist when listing models
  - [x] Fill `sizeBytes`, `localPath`, `createdAt`, and `updatedAt`
  - [x] Mark missing files as `missing` or failed entries as `failed`

- [x] Implement Hugging Face model downloads
  - [x] Validate `DownloadRequest` fields
  - [x] Build the Hugging Face resolve URL with repo, revision, and filename
  - [x] Add optional `Authorization: Bearer` header for private models
  - [x] Download to a temporary partial file
  - [x] Track `downloadedBytes`, `sizeBytes`, and state updates
  - [x] Atomically rename completed files into the models directory
  - [x] Persist metadata on start, progress milestones, completion, and failure
  - [x] Support `CancelModelDownload` with `context.CancelFunc`
  - [x] Clean up partial files after cancellation or failure

- [x] Implement model file operations
  - [x] Implement `DeleteModel`
  - [x] Stop the server or reject deletion if the selected model is running
  - [x] Delete the local model file
  - [x] Remove or update the metadata record
  - [x] Implement `OpenModelsDir` for Windows, macOS, and Linux

- [x] Implement llama-server process management
  - [x] Decide how the backend locates `llama-server`
  - [x] Validate selected model exists and is ready
  - [x] Validate host, port, numeric parameters, and extra args
  - [x] Build process arguments without shell string concatenation
  - [x] Start `llama-server` with `exec.Command`
  - [x] Capture stdout and stderr into a bounded log tail
  - [x] Track PID, endpoint, model ID, model name, command line, and start time
  - [x] Implement `GetServerStatus`
  - [x] Implement graceful `StopLlamaServer`
  - [x] Handle process exit and update status automatically

- [x] Harden concurrency and error handling
  - [x] Protect shared model/download/server state with `sync.Mutex`
  - [x] Prevent duplicate downloads of the same repo/file
  - [x] Prevent multiple llama-server instances from this app
  - [x] Return actionable errors to the Wails frontend
  - [x] Avoid logging Hugging Face tokens
  - [x] Treat process-launch inputs as untrusted

- [x] Add backend tests
  - [x] Test metadata load/save behavior
  - [x] Test model ID and path generation
  - [x] Test Hugging Face URL construction
  - [x] Test server argument construction
  - [x] Test validation errors
  - [x] Run `go test ./...`

- [x] Regenerate bindings and verify integration
  - [x] Regenerate Wails bindings if exported method signatures change
  - [x] Verify `frontend/wailsjs/go/main/App.d.ts`
  - [x] Run `cd frontend && npm run build`
  - [ ] Manually verify with `wails dev`
  - [ ] Confirm frontend polling shows model and server status correctly
