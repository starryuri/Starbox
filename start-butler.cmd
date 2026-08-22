@echo off
setlocal
set "APP=C:\Users\27879\lobsterai\project\starbox.exe"
set "CFG=C:\Users\27879\lobsterai\project\config.json"
set "URL=http://127.0.0.1:8765/"
rem if server is already running, just open the window; otherwise start everything + window
curl -s -o NUL -m 3 "%URL%health" 2>nul
if errorlevel 1 (
  start "" "%APP%" -config "%CFG%" -desktop
) else (
  start "" "%APP%" -window
)
endlocal
