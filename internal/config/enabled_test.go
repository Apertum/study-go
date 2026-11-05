package config

import (
	"strings"
	"testing"
)

func TestParseEnabled(t *testing.T) {
	tests := []struct {
		name      string
		envValue  string
		wantErr   string
		flagValue bool
		want      bool
	}{
		{name: "пустая переменная сохраняет флаг выключено", flagValue: false, envValue: "", want: false},
		{name: "пустая переменная сохраняет флаг включено", flagValue: true, envValue: "", want: true},
		{name: "единица включает", flagValue: false, envValue: "1", want: true},
		{name: "краткая t включает", flagValue: false, envValue: "t", want: true},
		{name: "TRUE включает", flagValue: false, envValue: "TRUE", want: true},
		{name: "true включает", flagValue: false, envValue: "true", want: true},
		{name: "True включает", flagValue: false, envValue: "True", want: true},
		{name: "ноль выключает", flagValue: true, envValue: "0", want: false},
		{name: "краткая f выключает", flagValue: true, envValue: "f", want: false},
		{name: "FALSE выключает", flagValue: true, envValue: "FALSE", want: false},
		{name: "false выключает", flagValue: true, envValue: "false", want: false},
		{name: "False выключает", flagValue: true, envValue: "False", want: false},
		{name: "мусорное значение ошибочно", flagValue: false, envValue: "yes-please", wantErr: `ENABLE_HTTPS="yes-please"`},
		{name: "слово on ошибочно", flagValue: true, envValue: "on", wantErr: `ENABLE_HTTPS="on"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEnabled(tt.flagValue, tt.envValue)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ожидалась ошибка для %q, получили значение %v", tt.envValue, got)
				}

				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("в тексте %q нет %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}

			if got != tt.want {
				t.Errorf("флаг=%v переменная=%q: получили %v, ожидали %v", tt.flagValue, tt.envValue, got, tt.want)
			}
		})
	}
}
