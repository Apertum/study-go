#!/bin/bash
# Скрипт для запуска бенчмарков, сбора CPU-профилей и сравнения результатов.
#
# Использование:
#   bash scripts/run_benchmarks.sh          # запуск и генерация базового профиля
#   bash scripts/run_benchmarks.sh result   # после изменений — генерация результата
#
# Результат: profiles/base.pprof / profiles/result.pprof + README-фрагмент

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROFILES_DIR="$PROJECT_DIR/profiles"
SCRIPTS_DIR="$PROJECT_DIR/scripts"
HANDLER_PKG="study-go.ru/cho/eto/internal/handler"

mkdir -p "$PROFILES_DIR" "$SCRIPTS_DIR"

MODE="${1:-base}"

echo "=== Запуск бенчмарков: $MODE ==="
cd "$PROJECT_DIR"

# Собираем benchmark-результаты
BENCH_OUTPUT=$(go test -bench=Benchmark -benchmem -count=1 -cpuprofile="$PROFILES_DIR/${MODE}.pprof" ./internal/handler/ 2>&1 | \
    grep -E "^Benchmark|^goos|^goarch|^cpu|^PASS|^ok|ns/op|B/op|allocs/op" || true)

echo "$BENCH_OUTPUT"

# Генерируем фрагмент для README
cat <<EOF
## Результаты бенчмарков ($MODE)

\`\`\`
$BENCH_OUTPUT
\`\`\`

EOF

# Сохраняем результаты бенчмарков в текстовый файл
go test -bench=. -benchmem -count=1 ./internal/handler/ 2>&1 | \
    grep -E "^Benchmark|ns/op|B/op|allocs/op" > "$PROFILES_DIR/benchmark_${MODE}.txt" 2>/dev/null || true

echo "Профиль сохранён: profiles/${MODE}.pprof"
echo "Текстовый результат: profiles/benchmark_${MODE}.txt"
