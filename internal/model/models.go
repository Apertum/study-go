package models

const (
	TypeSimpleUtterance = "SimpleUtterance"
)

// Request описывает запрос пользователя.
// см. https://yandex.ru/dev/dialogs/alice/doc/request.html
type Request struct {
	Request  SimpleUtterance `json:"request"`
	Version  string          `json:"version"`
	Session  Session         `json:"session"`
	Timezone string          `json:"timezone"`
}

// SimpleUtterance описывает команду, полученную в запросе типа SimpleUtterance.
type SimpleUtterance struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// Response описывает ответ сервера.
// см. https://yandex.ru/dev/dialogs/alice/doc/response.html
type Response struct {
	Response ResponsePayload `json:"response"`
	Version  string          `json:"version"`
	Timezone string          `json:"timezone"`
	Session  Session         `json:"session"`
}

// Session описывает данные о сессии пользователя.
type Session struct {
	ID   string      `json:"id"`
	New  bool        `json:"new"`
	User RequestUser `json:"user"`
}

// RequestUser содержит данные об авторизованном пользователе навыка
type RequestUser struct {
	UserID string `json:"user_id"`
}

// ResponsePayload описывает ответ, который нужно озвучить.
type ResponsePayload struct {
	Text string `json:"text"`
}
