package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Секретные значения для тестов журнала: код разбора файла не имеет права их напечатать.
const (
	secretCookie = "секретный-ключ-из-файла"
	secretDSN    = "postgres://user:parol-iz-fayla@host:5432/db"
)

// valueDiagnostics — строки журнала, которые ParseFlags печатал и до файла: они показывают
// итоговые значения настроек независимо от источника и этим изменением не тронуты.
var valueDiagnostics = []string{"Read addr:", "Read base:", "DSN для подключения к PostgreSQL:"}

// writeConfig пишет фикстуру во временный каталог и возвращает путь к файлу.
func writeConfig(t *testing.T, content string) string {
	t.Helper()

	return writeConfigNamed(t, "config.json", content)
}

// writeConfigNamed пишет фикстуру под своим именем: несколько файлов в одном тесте не сталкиваются.
func writeConfigNamed(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("фикстура %s не записана: %v", name, err)
	}

	return path
}

func TestFileConfigJSONTags(t *testing.T) {
	want := []string{
		"server_address", "base_url", "file_storage_path", "database_dsn", "cookie_key",
		"audit_file", "audit_url", "enable_https", "cert_file", "key_file", "http_server_address",
	}

	var got []string

	for field := range reflect.TypeFor[fileConfig]().Fields() {
		got = append(got, field.Tag.Get("json"))
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("теги %v, ожидали %v", got, want)
	}
}

func TestReadFileConfigOwnerExample(t *testing.T) {
	doc, err := readFileConfig(filepath.Join("testdata", "config.example.json"))
	if err != nil {
		t.Fatalf("профиль владельца не разобрался: %v", err)
	}

	if doc.ServerAddress != "localhost:8080" {
		t.Errorf("адрес %q, ожидали localhost:8080", doc.ServerAddress)
	}

	if doc.EnableHTTPS == nil || !*doc.EnableHTTPS {
		t.Errorf("enable_https %v, ожидали true", doc.EnableHTTPS)
	}

	// Профиль репозитория без секретов: ключа подписи там нет, DSN пустой.
	if doc.CookieKey != "" {
		t.Errorf("cookie_key %q, ожидали отсутствия ключа", doc.CookieKey)
	}

	if doc.DatabaseDSN != "" {
		t.Errorf("database_dsn %q, ожидали пустого", doc.DatabaseDSN)
	}
}

// TestReadFileConfigIsStrictJSON фиксирует, что препроцессора у файла нет: комментарии и
// хвостовые запятые делают файл неразобранным, а `//` внутри значения остаётся его частью.
func TestReadFileConfigIsStrictJSON(t *testing.T) {
	doc, err := readFileConfig(writeConfig(t, `{"base_url": "http://localhost"}`))
	if err != nil {
		t.Fatalf("слэши внутри значения сломали разбор: %v", err)
	}

	if doc.BaseURL != "http://localhost" {
		t.Errorf("базовый префикс %q, ожидали http://localhost", doc.BaseURL)
	}

	notJSON := []struct {
		name    string
		content string
	}{
		{"строчный комментарий после значения", "{\n\"server_address\": \":8080\", // аналог SERVER_ADDRESS\n}"},
		{"комментарий в конце файла", `{"server_address": ":8080"} // хвост`},
		{"файл из одних комментариев", "// настройки нет\n// и тут нет\n"},
		{"хвостовая запятая", `{"server_address": ":8080",}`},
	}

	for _, tt := range notJSON {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readFileConfig(writeConfig(t, tt.content)); err == nil {
				t.Fatal("ожидали ошибку разбора: расширение синтаксиса JSON не поддерживается")
			}
		})
	}
}

func TestReadFileConfigErrors(t *testing.T) {
	t.Run("несуществующий путь", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "нет-такого.json")

		_, err := readFileConfig(path)
		if err == nil {
			t.Fatal("ожидали ошибку чтения отсутствующего файла")
		}

		if !strings.Contains(err.Error(), path) {
			t.Errorf("в ошибке %q нет пути %q", err, path)
		}
	})

	t.Run("каталог вместо файла", func(t *testing.T) {
		if _, err := readFileConfig(t.TempDir()); err == nil {
			t.Fatal("ожидали ошибку чтения каталога")
		}
	})

	broken := []struct {
		name    string
		content string
	}{
		{"битый JSON", `{"server_address": `},
		{"документ-массив", `[1,2,3]`},
		{"документ-строка", `"строка"`},
		{"логическое значение в кавычках", `{"enable_https": "true"}`},
		{"блок-комментарий", `{"server_address": ":9099" /* профиль */}`},
	}

	for _, tt := range broken {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readFileConfig(writeConfig(t, tt.content)); err == nil {
				t.Fatal("ожидали ошибку разбора")
			}
		})
	}

	t.Run("ошибка не содержит содержимого файла", func(t *testing.T) {
		_, err := readFileConfig(writeConfig(t, `{"cookie_key": "`+secretCookie+`", "server_address": `))
		if err == nil {
			t.Fatal("ожидали ошибку разбора")
		}

		if strings.Contains(err.Error(), secretCookie) || strings.Contains(err.Error(), "cookie_key") {
			t.Errorf("в тексте ошибки осталось содержимое файла: %q", err)
		}
	})
}

func TestReadFileConfigEmptyDocumentIsNotError(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"пустой файл", ``},
		{"пробелы", "   \n\t\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := readFileConfig(writeConfig(t, tt.content))
			if err != nil {
				t.Fatalf("пустой документ стал ошибкой: %v", err)
			}

			if doc != (fileConfig{}) {
				t.Errorf("документ %+v, ожидали нулевой", doc)
			}
		})
	}
}

func TestReadFileConfigUnknownKeys(t *testing.T) {
	doc, err := readFileConfig(writeConfig(t, `{
		"unknown_setting": "x",
		"ServerExtra": 1,
		"server_address": "localhost:8080"
	}`))
	if err != nil {
		t.Fatalf("неизвестные ключи сломали разбор: %v", err)
	}

	if doc.ServerAddress != "localhost:8080" {
		t.Errorf("адрес %q, ожидали значение из файла", doc.ServerAddress)
	}
}

func TestParseFlagsBrokenFileWarnsAndKeepsSettings(t *testing.T) {
	var log bytes.Buffer

	path := writeConfig(t, `{"server_address": "из-файла:9999", "audit_file": "из-файла.jsonl"`)

	err := runParse(t, []string{"-c", path, "-b", "http://flag/"}, map[string]string{"AUDIT_URL": "https://env/"}, &log)
	if err != nil {
		t.Fatalf("битый файл не должен прерывать запуск: %v", err)
	}

	if out := log.String(); !strings.Contains(out, "конфигурация из файла не применена") || !strings.Contains(out, path) {
		t.Errorf("в журнале %q нет предупреждения с путём %q", out, path) // todo check test in Windows
	}

	// Ни одна настройка из неразобранного файла не применена.
	if Addr != ":8080" || AuditFile != "" {
		t.Errorf("адрес %q и файл аудита %q, ожидали значений по умолчанию", Addr, AuditFile)
	}

	// Старшие источники работают как ни в чём не бывало.
	if BaseURL != "http://flag/" {
		t.Errorf("base url %q, ожидали значение флага", BaseURL)
	}

	if AuditURL != "https://env/" {
		t.Errorf("URL аудита %q, ожидали значение переменной", AuditURL)
	}
}

func TestParseFlagsMissingFileWarnsAndKeepsSettings(t *testing.T) {
	var log bytes.Buffer

	path := filepath.Join(t.TempDir(), "нет-такого.json")

	if err := runParse(t, []string{"-c", path}, nil, &log); err != nil {
		t.Fatalf("отсутствующий файл не должен прерывать запуск: %v", err)
	}

	out := log.String()
	if !strings.Contains(out, "конфигурация из файла не применена") || !strings.Contains(out, path) {
		t.Errorf("в журнале %q нет предупреждения с путём %q", out, path) // todo check test in Windows
	}

	if Addr != ":8080" || BaseURL != "http://short.ru/" {
		t.Errorf("адрес %q либо базовый префикс %q взяты не из значений по умолчанию", Addr, BaseURL)
	}
}

func TestParseFlagsBrokenFileAppliesNothingPartially(t *testing.T) {
	var log bytes.Buffer

	path := writeConfig(t, `{"server_address": "половина:9999", "file_storage_path": "половина.json", "cookie_key": "`+secretCookie+`", ???`)

	if err := runParse(t, []string{"-c", path}, nil, &log); err != nil {
		t.Fatalf("запуск не должен прерываться из-за файла: %v", err)
	}

	if !strings.Contains(log.String(), "конфигурация из файла не применена") {
		t.Errorf("предупреждения о неразобранном файле нет: %q", log.String())
	}

	if Addr != ":8080" || FileName != "data.json" || CookieKey != "thisSuoerSecretMyKey" {
		t.Errorf("полудокумент применён: addr=%q файл=%q ключ=%q", Addr, FileName, CookieKey)
	}
}

func TestParseFlagsFileContentStaysOutOfLog(t *testing.T) {
	var log bytes.Buffer

	path := writeConfig(t, `{"cookie_key": "`+secretCookie+`", "database_dsn": "`+secretDSN+`"}`)
	if err := runParse(t, []string{"-c", path}, nil, &log); err != nil {
		t.Fatalf("конфигурация не разобрана: %v", err)
	}

	out := log.String()

	for line := range strings.SplitSeq(out, "\n") {
		if isValueDiagnostics(line) {
			continue
		}

		if strings.Contains(line, secretCookie) || strings.Contains(line, "parol-iz-fayla") || strings.Contains(line, "cookie_key") {
			t.Errorf("код разбора напечатал содержимое файла: %q", line)
		}
	}
}

// TestParseFlagsValueDiagnosticsUnchanged фиксирует, что три существующие строки журнала
// остались: изменение их не трогало.
func TestParseFlagsValueDiagnosticsUnchanged(t *testing.T) {
	var log bytes.Buffer

	if err := runParse(t, nil, nil, &log); err != nil {
		t.Fatalf("конфигурация не разобрана: %v", err)
	}

	for _, want := range valueDiagnostics {
		if !strings.Contains(log.String(), want) {
			t.Errorf("строка диагностики %q исчезла из журнала: %q", want, log.String())
		}
	}
}

// isValueDiagnostics говорит, что строка принадлежит предсуществующим строкам,
// которые печатают итоговые значения настроек.
func isValueDiagnostics(line string) bool {
	for _, marker := range valueDiagnostics {
		if strings.Contains(line, marker) {
			return true
		}
	}

	return false
}
