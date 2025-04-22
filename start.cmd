@echo off
REM Detect and pick the Windows binary
if exist better-docs-server-windows-amd64.exe (
  set "BIN=better-docs-server-windows-amd64.exe"
) else if exist better-docs-server-windows-arm64.exe (
  set "BIN=better-docs-server-windows-arm64.exe"
) else (
  echo ERROR: no suitable Windows binary found.
  exit /b 1
)

REM Run it with any passed arguments
"%~dp0%BIN%" %*