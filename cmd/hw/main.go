package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	fmt.Println("0 Hello, Go main function!")

	response, err := http.Get("https://practicum.yandex.ru")
	if err != nil {
		fmt.Println(err)
	} else {
		contentType := response.Header.Get("Content-Type")
		// это может быть, например, "application/json; charset=UTF-8"
		fmt.Println(contentType)
	}
	fmt.Printf("Status Code: %d\r\n", response.StatusCode)
	for k, v := range response.Header {
		// заголовок может иметь несколько значений,
		// но для простоты запросим только первое
		fmt.Printf("%s: %v\r\n", k, v[0])
	}

	fmt.Println("1 Hello, Go main function!")

	defer response.Body.Close()
	if _, err = io.CopyN(os.Stdout, response.Body, 512); err != nil {
		fmt.Println(err)
	}

	fmt.Println("3 Hello, Go main function!")
	fmt.Println("4 Hello, Go main function!")
	fmt.Println("5 Hello, Go main function!")

	http.HandleFunc("/status", StatusHandler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func StatusHandler(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	// намеренно добавлена ошибка в JSON
	rw.Write([]byte(`{"status":"ok2"}`))
}

func GetAnyClient(url string) {
	client := &http.Client{}

	body := `{"id":"101"}`
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "multipart/form-data")
	response, err := client.Do(request)
	io.Copy(os.Stdout, response.Body)
	response.Body.Close()
}


