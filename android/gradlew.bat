@echo off
setlocal

set "ROOT_DIR=%~dp0"
if "%ROOT_DIR:~-1%"=="\" set "ROOT_DIR=%ROOT_DIR:~0,-1%"

if not "%WATCHER_ANDROID_SDK_ROOT%"=="" goto prepare
if not "%ANDROID_SDK_ROOT%"=="" goto prepare
if not "%ANDROID_HOME%"=="" goto prepare
if exist "%ROOT_DIR%\local.properties" goto run

:prepare
bash "%ROOT_DIR%\scripts\prepare-local-properties.sh"
if errorlevel 1 exit /b %errorlevel%

:run
for /f "usebackq delims=" %%i in (`bash "%ROOT_DIR%\scripts\install-gradle.sh"`) do set "GRADLE_BIN=%%i"
if not defined GRADLE_BIN (
  echo Failed to resolve Gradle bootstrap binary.
  exit /b 1
)

"%GRADLE_BIN%" %*
