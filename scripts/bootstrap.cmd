@echo off
REM By-hand entry point for Windows: double-clickable, and the one line INSTALL.md can tell a
REM user to run when they have no bash. The logic lives in bootstrap.ps1; this only reaches it
REM without asking the user to think about execution policy.
REM
REM Always exits 0, like both bootstrap scripts: a bootstrap must never break a session.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0bootstrap.ps1"
exit /b 0
