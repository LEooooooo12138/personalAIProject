package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	data, err := os.ReadFile("internal/core/session.go")
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	text := string(data)

	text = strings.Replace(text,
		"\tRole      string    // \"user\" 或 \"assistant\"",
		"\tRole      string    `json:\"role\"`    // \"user\" 或 \"assistant\"", 1)
	text = strings.Replace(text,
		"\tContent   string    // 消息正文",
		"\tContent   string    `json:\"content\"` // 消息正文", 1)
	text = strings.Replace(text,
		"\tTimestamp time.Time",
		"\tTimestamp time.Time `json:\"timestamp\"`", 1)

	if err := os.WriteFile("internal/core/session.go", []byte(text), 0644); err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("OK")
}
