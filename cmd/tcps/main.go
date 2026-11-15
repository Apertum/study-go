package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	// инициализируем TCP Listener
	l, err := net.Listen("tcp", "localhost:3333")
	if err != nil {
		fmt.Println("Error listening:", err)
		os.Exit(1)
	}
	// не забываем закрыть
	defer l.Close()
	for {
		// принимаем запросы на соединение
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting: ", err)
			continue
		}
		// обслуживаем соединение в горутине
		go handleConn(conn)
	}
}

// handleConn обслуживает соединение.
func handleConn(conn net.Conn) {
	// не забываем освободить ресурсы
	defer conn.Close()
	// аллокация буфера для чтения из соединения
	b := make([]byte, 1024)
	// читаем из соединения в буфер
	for {
		_, err := conn.Read(b)
		if err != nil {
			fmt.Println("Error reading:", err)
			return
		}
		fmt.Println("Received message:", string(b))
		// отвечаем респонденту
		_, err = conn.Write([]byte("Got it. \n"))
		if err != nil {
			fmt.Println("Error writing:", err)
			return
		}
	}
}

// *** *** *** *** ***

// handleConn обрабатывает запросы и вычисляет среднее арифметическое. Вместо main
func handleConnSum(c net.Conn) {
	defer c.Close()
	for {
		MaxLen := 1024
		in := make([]byte, MaxLen)
		n, err := c.Read(in)
		if err != nil {
			panic(err)
		}
		var sum int
		for i := 0; i < n; i++ {
			sum += int(in[i])
		}
		if _, err = c.Write([]byte{byte(sum / n)}); err != nil {
			panic(err)
		}
	}
}

// TCPServer запускает сервер и ожидает соединений.
func TCPServer(addr *net.TCPAddr) {
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			panic(err)
		}
		go handleConn(c)
	}
}
