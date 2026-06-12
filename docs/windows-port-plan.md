# Windows Port Implementation Plan

## Summary

Port `rigscope` to Windows with useful native telemetry, not just a compile-only build. The Windows build should run `rigscope serve`, collect CPU, memory, disk I/O, filesystem, network, process, and self-process metrics, serve the existing web UI, and pass the Go test suite on Windows.

Environment status: Go is installed at `C:\Program Files\Go\bin\go.exe` and reports `go1.26.4 windows/amd64`, which satisfies `go 1.24`. Git and PowerShell are installed. Nothing else needs installing, but some shells may need to be restarted before plain `go` is visible on `PATH`.

## Key Changes

- Finish platform separation:
  - Put Linux `/proc`, `/sys`, DRM, XDNA, thermal, power-supply, and zenpower collectors behind `//go:build linux`.
  - Keep common collector registry, sampler, metric helpers, NVIDIA/ROCM stubs, and shared flattening logic platform-neutral.
  - Move `syscall.Statfs`, workload `Setpgid`, and SIGTERM handling behind platform helpers.
- Add Windows native collectors:
  - Add `github.com/shirou/gopsutil/v4` as the Windows system telemetry dependency.
  - Register Windows collectors named `cpu`, `memory`, `disk`, `filesystem`, `network`, `process`, and `self`.
  - Emit metric names, units, symbols, labels, and kinds compatible with the existing dashboard presets.
  - Leave Windows GPU telemetry out of scope for this first port.
- Fix Windows runtime and test issues:
  - Add platform-specific signal helpers so Windows uses `os.Interrupt`, while Unix also includes `syscall.SIGTERM`.
  - Add `store.OpenInMemory(retention)` for tests that do not require disk persistence.
  - Convert non-persistence tests to in-memory storage to avoid Windows temp-dir cleanup failures from mapped tstorage files.
  - Keep production storage unchanged under `rigscope serve --data-dir`.
- Update developer experience:
  - Update README collector descriptions with Windows and Linux coverage.
  - Add Windows quick-start and test commands.
  - Add `rigscope-dev.ps1` for Windows development.

## Public Interfaces

- No HTTP API changes.
- No CLI command changes.
- Internal store API addition: `store.OpenInMemory(retention time.Duration) (*Store, error)`.
- New dependency in `go.mod`: `github.com/shirou/gopsutil/v4`.
- Collector registration expectations are platform-specific.

## Test Plan

- Run:
  ```powershell
  & 'C:\Program Files\Go\bin\go.exe' test ./...
  ```
- Build and smoke test:
  ```powershell
  & 'C:\Program Files\Go\bin\go.exe' run ./cmd/rigscope serve --data-dir data-windows-test
  & 'C:\Program Files\Go\bin\go.exe' run ./cmd/rigscope status
  ```
- Acceptance criteria:
  - `go test ./...` passes on Windows.
  - `rigscope serve` starts without `no collectors detected`.
  - `/api/metrics` returns Windows system metrics.
  - The dashboard loads at `http://127.0.0.1:7077`.

## Assumptions

- First Windows port prioritizes system telemetry and UI functionality; Windows GPU telemetry is out of scope.
- Persistent tstorage remains the production storage path; in-memory storage is only for tests that do not need disk persistence.
- No additional software installation is required beyond Go, Git, and PowerShell.
