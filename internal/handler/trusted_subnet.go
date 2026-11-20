package handler

import (
	"net"
	"net/http"
	"net/netip"

	"github.com/sirupsen/logrus"
)

// headerRealIP — заголовок, в который обратный прокси кладёт адрес клиента.
const headerRealIP = "X-Real-IP"

// TrustedSubnetGate ограничивает доступ к внутренним эндпоинтам доверенной подсетью.
//
// Проверка идёт в три шага:
//  1. Нулевой prefix — настройка не задана, и тогда доступ закрыт для любого запроса:
//     пустое значение означает «закрыто», а не «открыто для всех».
//  2. Адрес запроса берётся из X-Real-IP, если он передан ровно один раз и разбирается как
//     IP-адрес; иначе — из адреса TCP-соединения. Не разбирается ни один источник — отказ.
//  3. Адрес вне сети — отказ.
//
// Отвечает 403 без тела и выполняется раньше обработчика, поэтому отказ не раскрывает
// состояние источников данных эндпоинта. Кука авторизации роли не играет: сетевой гейт здесь
// единственный контроль доступа.
//
// X-Real-IP старше адреса соединения и тогда, когда соединение пришло извне доверенной сети.
// Это осознанный компромисс ради доступности статистики за обратным прокси; развёртывание
// обязано перезаписывать заголовок собой либо не задавать подсеть вовсе
// (см. раздел «Внутренние эндпоинты» в cmd/shorter/readme.md).
//
// Форма — посредник по образцу AuthMiddleware: навешивается точечно на подмаршруты /api/internal
// через chi Router.Use, а не входит в глобальный стек.
func TrustedSubnetGate(prefix netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addr, ok := requestAddr(r)

			if !prefix.IsValid() || !ok || !prefix.Contains(addr) {
				denyTrustedSubnet(w, r)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requestAddr возвращает адрес запроса и признак, что хоть один источник похож на IP-адрес:
// сначала X-Real-IP (ровно одно значение), затем хост соединения. Два значения заголовка —
// признаки подмены, такой заголовок не считается адресом.
func requestAddr(r *http.Request) (netip.Addr, bool) {
	if values := r.Header.Values(headerRealIP); len(values) == 1 {
		if addr, err := netip.ParseAddr(values[0]); err == nil {
			return addr.Unmap(), true
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// У RemoteAddr бывает и адрес без порта — например у запросов из httptest-двойника.
		host = r.RemoteAddr
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}

	return addr.Unmap(), true
}

// denyTrustedSubnet отвечает 403 с пустым телом. В журнал идёт только путь запроса: адреса
// клиентов и значение X-Real-IP сюда не попадают, чтобы эндпоинт статистики не стал логом доступа.
func denyTrustedSubnet(w http.ResponseWriter, r *http.Request) {
	logrus.WithField("path", r.URL.Path).Debug("доступ к внутреннему эндпоинту запрещён")

	w.WriteHeader(http.StatusForbidden)
}
