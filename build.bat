@echo off
set APP_NAME=matchSystem.exe
set MAIN_FILE=cmd\app\main.go

if "%1"=="build" goto build
if "%1"=="run" goto run
if "%1"=="clean" goto clean
if "%1"=="test" goto test

:build
echo Building %APP_NAME%...
go build -o bin\%APP_NAME% %MAIN_FILE%
goto end

:run
call :build
echo Running %APP_NAME%...
.\bin\%APP_NAME%
goto end

:clean
echo Cleaning up...
if exist bin rmdir /s /q bin
goto end

:test
echo Running tests...
go test .\...
goto end

:end
