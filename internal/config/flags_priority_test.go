package config

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// configUsage — справочный текст обоих имён флага пути к файлу.
const configUsage = "путь к JSON-файлу конфигурации"

// addrConfig — фикстура с одной настройкой адреса.
func addrConfig(addr string) string {
	return `{"server_address": "` + addr + `"}`
}

func TestParseFlagsConfigPathFlags(t *testing.T) {
	tests := []struct {
		name string
		want string
		args []string
	}{
		{"короткая форма", "./a.json", []string{"-c", "./a.json"}},
		{"длинная форма", "./b.json", []string{"-config", "./b.json"}},
		{"переданы обе формы — действует разобранная последней", "./b.json", []string{"-c", "./a.json", "-config", "./b.json"}},
		{"без флагов путь пуст", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseWith(t, tt.args, nil)

			if ConfigFile != tt.want {
				t.Errorf("путь %q, ожидали %q", ConfigFile, tt.want)
			}
		})
	}
}

func TestParseFlagsConfigPathHelp(t *testing.T) {
	parseWith(t, nil, nil)

	var names []string

	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		if f.Usage == configUsage {
			names = append(names, f.Name)
		}
	})

	want := []string{"c", "config"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("имена флага пути %v, ожидали %v", names, want)
	}

	var help bytes.Buffer

	flag.CommandLine.SetOutput(&help)
	flag.CommandLine.Usage()

	if got := strings.Count(help.String(), configUsage); got != len(want) {
		t.Errorf("в справке -h текст флага пути встречается %d раз, ожидали %d", got, len(want))
	}
}

func TestParseFlagsConfigPathPriority(t *testing.T) {
	// Адрес показывает, чей файл действительно прочитан.
	addrs := map[string]string{"a": "из-a:1111", "b": "из-b:2222"}

	tests := []struct {
		name string
		args func(a, b string) []string
		env  func(a, b string) map[string]string
		want string
	}{
		{
			name: "путь только из переменной",
			args: func(string, string) []string { return nil },
			env:  func(_, b string) map[string]string { return map[string]string{EnvConfig: b} },
			want: "b",
		},
		{
			name: "переменная переопределяет короткий флаг",
			args: func(a, _ string) []string { return []string{"-c", a} },
			env:  func(_, b string) map[string]string { return map[string]string{EnvConfig: b} },
			want: "b",
		},
		{
			name: "переменная переопределяет длинный флаг",
			args: func(_, b string) []string { return []string{"-config", b} },
			env:  func(a, _ string) map[string]string { return map[string]string{EnvConfig: a} },
			want: "a",
		},
		{
			name: "флаг работает без переменной",
			args: func(a, _ string) []string { return []string{"-c", a} },
			env:  func(string, string) map[string]string { return nil },
			want: "a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := writeConfigNamed(t, "a.json", addrConfig(addrs["a"]))
			b := writeConfigNamed(t, "b.json", addrConfig(addrs["b"]))

			parseWith(t, tt.args(a, b), tt.env(a, b))

			if Addr != addrs[tt.want] {
				t.Errorf("адрес %q, ожидали %q из фикстуры %q", Addr, addrs[tt.want], tt.want)
			}
		})
	}
}

func TestResolveString(t *testing.T) {
	const def = "значение флага"

	tests := []struct {
		name      string
		envValue  string
		flagValue string
		fileValue string
		want      string
		flagSet   bool
	}{
		{"ничего нет — значение флага", "", def, "", def, false},
		{"пустое значение флага по умолчанию", "", "", "", "", false},
		{"только файл", "", def, "из файла", "из файла", false},
		{"пустая строка в файле молчит", "", def, "", def, false},
		{"явный флаг старше файла", "", def, "из файла", def, true},
		{"явный флаг со значением по умолчанию старше файла", "", "", "из файла", "", true},
		{"переменная старше файла", "из переменной", def, "из файла", "из переменной", false},
		{"переменная старше явного флага", "из переменной", def, "из файла", "из переменной", true},
		{"пустая переменная молчит", "", def, "", def, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveString(tt.envValue, tt.flagSet, tt.flagValue, tt.fileValue)
			if got != tt.want {
				t.Errorf("resolveString(%q, %v, %q, %q) = %q, ожидали %q",
					tt.envValue, tt.flagSet, tt.flagValue, tt.fileValue, got, tt.want)
			}
		})
	}
}

func TestResolveBool(t *testing.T) {
	yes, no := true, false

	tests := []struct {
		fileValue *bool
		name      string
		envValue  string
		flagSet   bool
		flagValue bool
		want      bool
		wantErr   bool
	}{
		{name: "источники молчат — значение флага", flagValue: false, want: false},
		{name: "ключа в файле нет — значение флага", flagValue: true, fileValue: nil, want: true},
		{name: "только файл включает", fileValue: &yes, want: true},
		{name: "только файл выключает", flagValue: true, fileValue: &no, want: false},
		{name: "явный флаг старше файла", flagSet: true, flagValue: true, fileValue: &no, want: true},
		{name: "переменная старше явного флага", envValue: "false", flagSet: true, flagValue: true, want: false},
		{name: "переменная старше файла", envValue: "true", fileValue: &no, want: true},
		{name: "пустая переменная сохраняет файл", envValue: "", flagValue: false, fileValue: &yes, want: true},
		{name: "неразбираемая переменная — ошибка", envValue: "yes-please", flagValue: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveBool(tt.envValue, tt.flagSet, tt.flagValue, tt.fileValue)

			switch {
			case tt.wantErr && err == nil:
				t.Fatal("ожидали ошибку разбора значения переменной")
			case !tt.wantErr && err != nil:
				t.Fatalf("неожиданная ошибка: %v", err)
			}

			if got != tt.want {
				t.Errorf("resolveBool = %v, ожидали %v", got, tt.want)
			}
		})
	}
}

func TestParseFlagsAddressPriority(t *testing.T) {
	const fileAddr = "из-файла:9090"

	tests := []struct {
		env   map[string]string
		name  string
		input string
		want  string
		args  []string
	}{
		{
			name:  "только файл",
			input: addrConfig(fileAddr),
			want:  fileAddr,
		},
		{
			name:  "явный флаг старше файла",
			input: addrConfig(fileAddr),
			args:  []string{"-a", ":7070"},
			want:  ":7070",
		},
		{
			name:  "переменная старше файла",
			input: addrConfig(fileAddr),
			env:   map[string]string{"SERVER_ADDRESS": ":6060"},
			want:  ":6060",
		},
		{
			name:  "переменная старше и флага, и файла",
			input: addrConfig(fileAddr),
			args:  []string{"-a", ":7070"},
			env:   map[string]string{"SERVER_ADDRESS": ":6060"},
			want:  ":6060",
		},
		{
			name:  "флаг со значением по умолчанию всё равно явный",
			input: addrConfig(fileAddr),
			args:  []string{"-a", ":8080"},
			want:  ":8080",
		},
		{
			name:  "ключа в файле нет — значение по умолчанию",
			input: `{"base_url": "http://из-файла/"}`,
			want:  ":8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parseWith(t, append([]string{"-c", writeConfig(t, tt.input)}, tt.args...), tt.env)

			if Addr != tt.want {
				t.Errorf("адрес %q, ожидали %q", Addr, tt.want)
			}
		})
	}
}

func TestParseFlagsHTTPSPriority(t *testing.T) {
	tests := []struct {
		env  map[string]string
		name string
		key  string
		args []string
		want bool
	}{
		{name: "ключа нет — выключено", key: ""},
		{name: "включено только файлом", key: `"enable_https": true`, want: true},
		{name: "файл и флаг согласованы", key: `"enable_https": true`, args: []string{"-s"}, want: true},
		{name: "явный -s старше файла с false", key: `"enable_https": false`, args: []string{"-s"}, want: true},
		{name: "явный -s без ключа в файле", key: "", args: []string{"-s"}, want: true},
		{name: "переменная выключает то, что включил файл", key: `"enable_https": true`, env: map[string]string{EnvEnableHTTPS: "false"}},
		{name: "переменная выключает то, что включил флаг", key: "", args: []string{"-s"}, env: map[string]string{EnvEnableHTTPS: "0"}},
		{name: "переменная включает то, что не включил файл", key: `"enable_https": false`, env: map[string]string{EnvEnableHTTPS: "true"}, want: true},
		{name: "пустая переменная сохраняет флаг", key: "", args: []string{"-s"}, env: map[string]string{EnvEnableHTTPS: ""}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"-c", writeConfig(t, "{"+tt.key+"}")}, tt.args...)
			parseWith(t, args, tt.env)

			if EnableHTTPS != tt.want {
				t.Errorf("HTTPS включён=%v, ожидали %v", EnableHTTPS, tt.want)
			}
		})
	}
}

func TestParseFlagsEmptyFileValueKeepsDefault(t *testing.T) {
	parseWith(t, []string{"-c", writeConfig(t, `{"base_url": ""}`)}, nil)

	if BaseURL != "http://short.ru/" {
		t.Errorf("base url %q, ожидали значения по умолчанию", BaseURL)
	}
}

// TestParseFlagsFileFillsEverySetting сверяет профиль владельца с каждой переменной пакета:
// ключ с непустым значением обязан дойти до настройки, а отсутствующий — не трогать её.
func TestParseFlagsFileFillsEverySetting(t *testing.T) {
	path := filepath.Join("testdata", "config.example.json")

	doc, err := readFileConfig(path)
	if err != nil {
		t.Fatalf("профиль не разобрался: %v", err)
	}

	parseWith(t, []string{"-c", path}, nil)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"адрес", Addr, doc.ServerAddress},
		{"base url", BaseURL, doc.BaseURL},
		{"файл хранения", FileName, doc.FileStoragePath},
		{"DSN", DatabaseDSN, doc.DatabaseDSN},
		{"ключ куки без ключа в файле", CookieKey, "thisSuoerSecretMyKey"},
		{"файл аудита", AuditFile, doc.AuditFile},
		{"URL аудита", AuditURL, doc.AuditURL},
		{"сертификат", CertFile, doc.CertFile},
		{"закрытый ключ", KeyFile, doc.KeyFile},
		{"адрес редиректа", HTTPRedirectAddr, doc.HTTPServerAddress},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, ожидали %q", c.name, c.got, c.want)
		}
	}

	if !EnableHTTPS {
		t.Error("HTTPS не включён значением enable_https из файла")
	}
}

func TestParseFlagsWithoutConfigPathReadsNothing(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(addrConfig("из-файла:9999")), 0o600); err != nil {
		t.Fatalf("фикстура не записана: %v", err)
	}

	t.Chdir(dir)

	var log bytes.Buffer

	if err := runParse(t, nil, nil, &log); err != nil {
		t.Fatalf("конфигурация не разобрана: %v", err)
	}

	if Addr != ":8080" || FileName != "data.json" {
		t.Errorf("настройки %q и %q взяты из подхваченного файла", Addr, FileName)
	}

	if out := log.String(); strings.Contains(out, "конфигурация из файла") {
		t.Errorf("без пути к файлу предупреждений быть не должно: %q", out)
	}
}

func TestParseFlagsBadEnableHTTPSEnv(t *testing.T) {
	var log bytes.Buffer

	path := writeConfig(t, `{"server_address": "из-файла:9999", "enable_https": true, "audit_file": "из-файла.jsonl"}`)

	err := runParse(t, []string{"-c", path, "-s"}, map[string]string{
		EnvEnableHTTPS:   "yes-please",
		"SERVER_ADDRESS": ":6060",
	}, &log)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого значения переменной")
	}

	if !strings.Contains(err.Error(), EnvEnableHTTPS) || !strings.Contains(err.Error(), "yes-please") {
		t.Errorf("в ошибке %q нет имени и значения переменной", err)
	}

	// Прочие настройки не применяются: решение о завершении процесса принимает вызывающий код.
	if Addr != ":8080" || AuditFile != "" {
		t.Errorf("настройки применены до ошибки: адрес %q, файл аудита %q", Addr, AuditFile)
	}
}
