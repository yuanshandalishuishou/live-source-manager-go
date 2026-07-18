@echo off
REM ============================================================
REM  Live Source Manager (Go) - Windows 开机自启包装脚本
REM  本文件为【静态单一来源】：自动定位项目根，无需渲染。
REM  由任务计划程序 (LiveSourceManagerWeb) 调用。
REM  也可双击手动运行以测试启动逻辑。
REM
REM  相较 Python 版：直接运行编译好的单二进制 bin\lsm.exe，
REM  不再依赖 venv / uvicorn / nginx。
REM ============================================================
set "SCRIPT_DIR=%~dp0"
REM 项目根 = 本文件所在 deploy\windows 的上两级
pushd "%SCRIPT_DIR%..\.."
set "PROJECT_DIR=%CD%"
popd

set "LSM_BIN=%PROJECT_DIR%\bin\lsm.exe"
set "LOG_DIR=%PROJECT_DIR%\log"
set "LOG=%LOG_DIR%\windows_start.log"

if not exist "%LSM_BIN%" (
    echo [%date% %time%] [LiveSource] 二进制未找到: %LSM_BIN% >> "%LOG%"
    exit /b 1
)
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"

cd /d "%PROJECT_DIR%"
echo [%date% %time%] [LiveSource] 启动服务 (bin\lsm.exe --config-dir "%PROJECT_DIR%") >> "%LOG%"
"%LSM_BIN%" --config-dir "%PROJECT_DIR%" >> "%LOG%" 2>&1
echo [%date% %time%] [LiveSource] 服务进程退出 (exit=%errorlevel%) >> "%LOG%"
