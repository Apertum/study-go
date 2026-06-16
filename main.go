package main

import (
	"fmt"
	"os"
)

func main() {
    fmt.Println(os.Stderr, "0 Hello, Go main function!")
    fmt.Println(os.Stderr, "1 Hello, Go main function!")
    fmt.Println("3 Hello, Go main function!")
    fmt.Println("4 Hello, Go main function!")
    fmt.Println("5 Hello, Go main function!")
}
