# Скрипт для запуска бенчмарков, сбора CPU-профилей и сравнения результатов.
#
# Использование:
#   pwsh scripts/run_benchmarks.ps1          # запуск и генерация базового профиля
#   pwsh scripts/run_benchmarks.ps1 result   # после изменений — генерация результата
#
# Результат: profiles/base.pprof / profiles/result.pprof + README-фрагмент

$ErrorActionPreference = "Stop"

$PROJECT_DIR = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$PROFILES_DIR = Join-Path $PROJECT_DIR "profiles"
$SCRIPTS_DIR = Join-Path $PROJECT_DIR "scripts"
$HANDLER_PKG = "study-go.ru/cho/eto/internal/handler"

New-Item -ItemType Directory -Force -Path $PROFILES_DIR, $SCRIPTS_DIR | Out-Null

$MODE = if ($args.Count -gt 0) { $args[0] } else { "base" }

Write-Host "=== Запуск бенчмарков: $MODE ==="
Set-Location $PROJECT_DIR

# Собираем benchmark-результаты
$benchCmd = "go test -bench=Benchmark -benchmem -count=1 -cpuprofile=`"$PROFILES_DIR\$MODE.pprof`" ./internal/handler/"
$rawOutput = Invoke-Expression $benchCmd 2>&1

$BENCH_OUTPUT = $rawOutput | Where-Object { $_ -match "^Benchmark|^goos|^goarch|^cpu|^PASS|^ok|ns/op|B/op|allocs/op" }

$BENCH_OUTPUT | ForEach-Object { Write-Host $_ }

# Генерируем фрагмент для README
$readmeFragment = @"
## Результаты бенчмарков ($MODE)

```
$($BENCH_OUTPUT -join "`n")
```

"@

Write-Host $readmeFragment

# Сохраняем результаты бенчмарков в текстовый файл
$textCmd = "go test -bench=. -benchmem -count=1 ./internal/handler/"
Invoke-Expression $textCmd 2>&1 | Where-Object { $_ -match "^Benchmark|ns/op|B/op|allocs/op" } | Out-File -FilePath "$PROFILES_DIR\benchmark_$MODE.txt" -Encoding utf8 -ErrorAction SilentlyContinue

Write-Host "Профиль сохранён: profiles/$MODE.pprof"
Write-Host "Текстовый результат: profiles/benchmark_$MODE.txt"
