package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
)

// EnvConfig — имя переменной окружения с путём к JSON-файлу конфигурации.
// Непустое значение переопределяет флаги -c и -config, как и прочие настройки пакета.
const EnvConfig = "CONFIG"

// ConfigFile — путь к JSON-файлу конфигурации, заполняется флагами -c/-config или переменной CONFIG.
//
// Значения по умолчанию у пути нет намеренно: молча подхваченный ./config.json из рабочей
// директории был бы каналом случайного влияния чужого файла на запуск. Пустой путь означает
// «файл не используется» — операций с файловой системой и предупреждений тогда нет вовсе.
var ConfigFile string

// fileConfig — документ JSON-файла конфигурации: по полю на каждую действующую настройку сервера.
//
// Имена полей повторяют имена переменных окружения, поэтому соответствие «ключ файла | флаг | ENV»
// видно без словаря соответствий. Строковые поля без указателя: пустая строка означает
// «файл про настройку ничего не сказал» — то же правило, что для переменных окружения.
// Логическому полю указатель нужен, иначе false в файле был бы неотличим от отсутствующего ключа.
// Неизвестные ключи молча отбрасываются поведением encoding/json (DisallowUnknownFields не включаем).
type fileConfig struct {
	ServerAddress     string `json:"server_address"`
	BaseURL           string `json:"base_url"`
	FileStoragePath   string `json:"file_storage_path"`
	DatabaseDSN       string `json:"database_dsn"`
	CookieKey         string `json:"cookie_key"`
	AuditFile         string `json:"audit_file"`
	AuditURL          string `json:"audit_url"`
	EnableHTTPS       *bool  `json:"enable_https"`
	CertFile          string `json:"cert_file"`
	KeyFile           string `json:"key_file"`
	HTTPServerAddress string `json:"http_server_address"`
}

// readFileConfig читает файл и разбирает его целиком как строгий JSON: ни `//`-,
// ни блок-комментарии, ни хвостовые запятые не вырезаются — файл с ними не разобран.
// Ошибка содержит путь и причину, но не содержимое файла: там лежат секретные
// cookie_key и DSN с паролем.
//
// Пустой документ (пустой файл либо одни пробелы) — не ошибка, а «файл ни про что».
func readFileConfig(path string) (fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, fmt.Errorf("%s: не удалось прочитать файл: %w", path, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return fileConfig{}, nil
	}

	var doc fileConfig
	if err := json.Unmarshal(data, &doc); err != nil {
		return fileConfig{}, fmt.Errorf("%s: файл конфигурации не разобран как JSON: %w", path, err)
	}

	return doc, nil
}

// loadFileConfig возвращает разобранный документ: при пустом пути файл не читается вообще,
// при неудаче в журнал попадает одно предупреждение с способом задания пути, путём и причиной,
// а вызывающий код получает нулевой документ — то есть ни одна настройка не меняется.
//
// Завершать процесс здесь нельзя (анализатор noexit оставляет os.Exit только main): битый файл
// не срывает запуск, а флаги и переменные окружения остаются рабочим способом задать настройки.
func loadFileConfig(path, source string) fileConfig {
	if path == "" {
		return fileConfig{}
	}

	doc, err := readFileConfig(path)
	if err != nil {
		logrus.Warnf("конфигурация из файла не применена: %s: %v", source, err)

		return fileConfig{}
	}

	return doc
}

// configSource возвращает имя способа задания пути к файлу — его печатают предупреждения.
func configSource(envValue string, set map[string]bool) string {
	switch {
	case envValue != "":
		return EnvConfig
	case set["config"]:
		return "-config"
	default:
		return "-c"
	}
}
