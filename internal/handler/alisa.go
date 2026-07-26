package handler

import (
	"encoding/json"
	"net/http"

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

	// заполняем модель ответа
	resp := models.Response{
		Response: models.ResponsePayload{
			Text: "Извините, я пока ничего не умею",
		},
		Version: "1.0",
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
