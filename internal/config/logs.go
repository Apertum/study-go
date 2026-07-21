package config

import (
	"fmt"
	"io"
	"os"
	"unicode"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

func LogsInit() {

	// установим уровень логирования
	logrus.SetLevel(logrus.TraceLevel)

	// установим форматирование логов для консоли: 2026-07-12T12:00:00Z [info] "msg"
	logrus.SetFormatter(&consoleFormatter{})

	logFile := "logs/app.log"
	// 1. Создаем папку для логов, если её нет
	err := os.MkdirAll("./logs", 0755)
    if err != nil {
		logrus.WithError(err).Fatal("Не удалось создать директорию для логов")
	}

	// 2. Настраиваем lumberjack для ротации логов (лимит 10MB на файл)
	logger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    1, // максимум 1 МБ на файл
		MaxAge:     7, // хранить до 7 старых файлов
		Compress:   true, // сжимать старые файлы
		LocalTime:  true, // использовать локальное время в именах
	}
	if err := logger.Close(); err != nil {
		logrus.WithError(err).Warn("Ошибка при инициализации lumberjack")
	}

	// 3. Добавляем хук, который дублирует логи в файл с ротацией в JSON формате
	logrus.AddHook(&jsonFileHook{
		Writer: logger,
		LogLevels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
			logrus.WarnLevel,
			logrus.InfoLevel,
			//logrus.DebugLevel,
			//logrus.TraceLevel,
		},
	})
	logrus.Info("Удалось настроить файл логов с ротацией (10MB limit)!")
}

// consoleFormatter — форматер для консоли: 2026-07-12T12:00:00Z [info] "msg"
type consoleFormatter struct{}

func (f *consoleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	level := entry.Level
	levelStr := level.String()
	if levelStr == "" {
		levelStr = "info"
	}
	msg := entry.Message
	timestamp := entry.Time.Format("02.01_15:04:05.000") // психоделика
	return []byte(fmt.Sprintf("%s [%s] \"%s\"\n", timestamp, string(unicode.ToUpper([]rune(levelStr)[0])), msg)), nil
}

// jsonFileHook — хук для logrus, который пишет логи в файл в JSON формате
type jsonFileHook struct {
	Writer    io.Writer
	LogLevels []logrus.Level
}

func (h *jsonFileHook) Levels() []logrus.Level {
	return h.LogLevels
}

func (h *jsonFileHook) Fire(entry *logrus.Entry) error {
	formatter := &logrus.JSONFormatter{}
	bytes, err := formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = h.Writer.Write(bytes)
	return err
}


