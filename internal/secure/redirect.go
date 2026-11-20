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
//
// Текст ошибки сохранён с момента появления проверки: по нему различают, какой способ задания
// править.
func AddrConflict(mainAddr, redirectAddr string) error {
	if !sameListenAddr(mainAddr, redirectAddr) {
		return nil
	}

	return fmt.Errorf("адрес редиректа %s совпадает с основным адресом %s: оба слушателя не поместятся", redirectAddr, mainAddr)
}

// ListenAddr — адрес слушателя вместе с ролью и способами задания настройки: сообщение о
// конфликте обязано называть оба адреса так, чтобы было понятно, какую настройку править.
type ListenAddr struct {
	// Role описывает слушателя в сообщении — «адрес gRPC-слушателя».
	Role string
	// Setting перечисляет способы задания — «-grpc-addr/GRPC_ADDRESS/grpc_address».
	Setting string
	// Addr — итоговый адрес слушателя.
	Addr string
}

// AddrConflictAll проверяет произвольный набор адресов слушателей одного процесса — HTTP,
// редирект, gRPC. Ошибкой считается любая пара, которая встанет на один порт: пустой хост
// означает «все интерфейсы», поэтому он пересекается с любым явно указанным хостом.
func AddrConflictAll(addrs ...ListenAddr) error {
	for i := range addrs {
		for j := i + 1; j < len(addrs); j++ {
			if sameListenAddr(addrs[i].Addr, addrs[j].Addr) {
				return fmt.Errorf("%s (%s) %s совпадает с %s (%s) %s: оба слушателя не поместятся",
					addrs[i].Role, addrs[i].Setting, addrs[i].Addr,
					addrs[j].Role, addrs[j].Setting, addrs[j].Addr)
			}
		}
	}

	return nil
}

// sameListenAddr говорит, что два адреса не поместятся одновременно.
func sameListenAddr(first, second string) bool {
	firstHost, firstPort := splitHostPort(first)
	secondHost, secondPort := splitHostPort(second)

	if firstPort != secondPort {
		return false
	}

	return firstHost == "" || secondHost == "" || firstHost == secondHost
}

// splitHostPort разбирает адрес слушателя; без порта возвращается пустой порт и весь адрес как хост.
func splitHostPort(addr string) (host, port string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, ""
	}

	return h, p
}
