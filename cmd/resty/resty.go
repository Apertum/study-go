package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

func main() {
	var users []User
	url := "https://jsonplaceholder.typicode.com/users"

	client := resty.New()

	_, err := client.R().SetResult(&users).Get(url)
	if err != nil {
		panic(err)
	}
	var out []string
	for _, v := range users {
		out = append(out, v.Username)
	}
	fmt.Println(strings.Join(out, ` `))
}

func resty2() {
	client := resty.New()

	client.
		// устанавливаем количество повторений
		SetRetryCount(3).
		// длительность ожидания между попытками
		SetRetryWaitTime(30 * time.Second).
		// длительность максимального ожидания
		SetRetryMaxWaitTime(90 * time.Second)

	resp, err := client.R().
		SetHeader("Content-Type", "application/json").
		SetBody(`{"title":"foo", "body":"bar", "userId": 7}`).
		Post("https://jsonplaceholder.typicode.com/posts")

	if err != nil {
		panic(err)
	}
	fmt.Println(resp)

	// другой вариант POST-запроса
	// если передаётся map, то по умолчанию используется JSON
	resp, err = client.R().
		SetBody(map[string]interface{}{"title": "My title", "body": "Content", "userId": 7}).
		Post("https://jsonplaceholder.typicode.com/posts")

	if err != nil {
		panic(err)
	}
	fmt.Println(resp)
}
