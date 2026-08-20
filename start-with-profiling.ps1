#!/usr/bin/env pwsh

# Скрипт для запуска приложения с профилированием
# Использование: .\start-with-profiling.ps1 [options]

param(
    [switch]$EnableProfiling = $false,
    [switch]$EnableCPU = $false,
    [string]$PprofPort = "6060",
    [string]$ProfileInterval = "10m",
    [switch]$Help = $false
)

if ($Help) {
    Write-Host @"
Запуск air_orc с профилированием

Использование: .\start-with-profiling.ps1 [options]

Опции:
  -EnableProfiling    Включить профилирование (по умолчанию: false)
  -EnableCPU          Включить CPU профилирование при старте (по умолчанию: false)
  -PprofPort          Порт для pprof HTTP сервера (по умолчанию: 6060)
  -ProfileInterval    Интервал автосохранения профилей (по умолчанию: 10m)
  -Help               Показать эту справку

Примеры:
  .\start-with-profiling.ps1 -EnableProfiling
  .\start-with-profiling.ps1 -EnableProfiling -EnableCPU
  .\start-with-profiling.ps1 -EnableProfiling -PprofPort 7070

После запуска:
  - Откройте http://localhost:$PprofPort/debug/pprof/ для веб-интерфейса
  - Профили сохраняются в ./profiles/
  - Подробнее: PROFILING.md
"@
    exit 0
}

# Проверяем наличие исполняемого файла
if (-not (Test-Path ".\test.exe")) {
    Write-Host "❌ Файл test.exe не найден" -ForegroundColor Red
    Write-Host "Сначала выполните сборку: go build -o test.exe .\cmd\" -ForegroundColor Yellow
    exit 1
}

# Создаем директорию для профилей
if (-not (Test-Path ".\profiles")) {
    New-Item -ItemType Directory -Path ".\profiles" | Out-Null
    Write-Host "✅ Создана директория ./profiles/" -ForegroundColor Green
}

# Формируем команду запуска
$args = @()

if ($EnableProfiling) {
    $args += "-profile"
    Write-Host "✅ Профилирование включено" -ForegroundColor Green

    if ($EnableCPU) {
        $args += "-cpu-profile"
        Write-Host "✅ CPU профилирование включено" -ForegroundColor Green
    }

    $args += "-pprof-port=$PprofPort"
    Write-Host "✅ pprof сервер: http://localhost:$PprofPort/debug/pprof/" -ForegroundColor Green

    $args += "-profile-interval=$ProfileInterval"
    Write-Host "✅ Автосохранение профилей: каждые $ProfileInterval" -ForegroundColor Green

    Write-Host ""
    Write-Host "📊 Полезные команды:" -ForegroundColor Cyan
    Write-Host "   go tool pprof http://localhost:$PprofPort/debug/pprof/heap" -ForegroundColor Gray
    Write-Host "   go tool pprof http://localhost:$PprofPort/debug/pprof/goroutine" -ForegroundColor Gray
    Write-Host "   go tool pprof http://localhost:$PprofPort/debug/pprof/profile?seconds=30" -ForegroundColor Gray
    Write-Host ""
} else {
    Write-Host "ℹ️  Профилирование отключено. Используйте -EnableProfiling для включения" -ForegroundColor Yellow
}

Write-Host "🚀 Запуск приложения..." -ForegroundColor Green
Write-Host ""

# Запускаем приложение
& ".\test.exe" @args

