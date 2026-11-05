package secure

import (
	"fmt"
	"net"
	"net/http"
)

// RedirectHandler отдаёт любой запрос постоянным редиректом 301 на HTTPS: схема https,
// хост из запроса без порта редиректа, порт основного TLS-адреса, путь и строка запроса
// сохранены. Если заголовок Host пуст, берётся хост из tlsAddr, а при пустом хосте — localhost.
func RedirectHandler(tlsAddr string) http.Handler {
	tlsHost, tlsPort := splitHostPort(tlsAddr)

	// Запасной хост считается один раз: обработчик живёт в горутине и не должен менять захваченные значения.
	fallbackHost := tlsHost
	if fallbackHost == "" {
		fallbackHost = localhost
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}

		if host == "" {
			host = fallbackHost
		}

		authority := host
		if tlsPort != "" {
			authority = net.JoinHostPort(host, tlsPort)
		}

		http.Redirect(w, r, "https://"+authority+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// AddrConflict возвращает ошибку, если адрес редиректа занял бы тот же адрес, что основной
// сервер: один из хостов не указан либо хосты равны при совпадающем порту.
func AddrConflict(mainAddr, redirectAddr string) error {
	mainHost, mainPort := splitHostPort(mainAddr)
	redirectHost, redirectPort := splitHostPort(redirectAddr)

	if mainPort != redirectPort {
		return nil
	}

	if mainHost != "" && redirectHost != "" && mainHost != redirectHost {
		return nil
	}

	return fmt.Errorf("адрес редиректа %s совпадает с основным адресом %s: оба слушателя не поместятся", redirectAddr, mainAddr)
}

// splitHostPort разбирает адрес слушателя; без порта возвращается пустой порт и весь адрес как хост.
func splitHostPort(addr string) (host, port string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, ""
	}

	return h, p
}
