@echo off
echo.
echo === PASO 1: Compilando AgentSNMP para Windows x64 ===
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1
go build -ldflags="-H windowsgui" -o cmd/agent/AgentSNMP.exe ./cmd/agent/
if %ERRORLEVEL% NEQ 0 (
    echo ERROR: La compilacion de Go fallo.
    pause
    exit /b 1
)
echo OK: AgentSNMP.exe generado en cmd/agent/

echo.
echo === PASO 2: Generando instalador con Inno Setup ===
set ISCC="C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
if not exist %ISCC% set ISCC="C:\Program Files\Inno Setup 6\ISCC.exe"
if not exist %ISCC% (
    echo AVISO: Inno Setup no encontrado. Genera el instalador manualmente desde Inno Setup IDE.
    echo        Script: cmd\agent\setup_script.iss
    pause
    exit /b 0
)
%ISCC% cmd\agent\setup_script.iss
if %ERRORLEVEL% NEQ 0 (
    echo ERROR: Inno Setup fallo al generar el instalador.
    pause
    exit /b 1
)
echo.
echo OK: Instalador generado en cmd\agent\Output\
pause
