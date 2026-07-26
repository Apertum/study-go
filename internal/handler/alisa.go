package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	models "study-go.ru/cho/eto/internal/model"
)

// функция webhook —
func Webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		logrus.Debug("got request with bad method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// десериализуем запрос в структуру модели
	logrus.Debug("decoding request")
	var req models.Request
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		logrus.Debug("cannot decode request JSON body", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// проверяем, что пришёл запрос понятного типа
	if req.Request.Type != models.TypeSimpleUtterance {
		logrus.Debug("unsupported request type " + req.Request.Type)
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	text := "Для вас нет новых сообщений."
	// первый запрос новой сессии
	if req.Session.New {
		// обрабатываем поле Timezone запроса
		tz, err := time.LoadLocation(req.Timezone)
		if err != nil {
			logrus.Debug("cannot parse timezone")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// получаем текущее время в часовом поясе пользователя
		now := time.Now().In(tz)
		hour, minute, _ := now.Clock()

		// формируем текст ответа
		text = fmt.Sprintf("Точное время %d часов, %d минут. %s", hour, minute, text)
	}

	// заполняем модель ответа
	resp := models.Response{
		Response: models.ResponsePayload{
			Text: text,
		},
		Version:  "1.0",
		Timezone: "Europe/Moscow", // Пример значения для Timezone
	}

	w.Header().Set("Content-Type", "application/json")

	// сериализуем ответ сервера
	enc := json.NewEncoder(w)
	if err := enc.Encode(resp); err != nil {
		logrus.Debug("error encoding response", err)
		return
	}
	logrus.Debug("sending HTTP 200 response")
}
