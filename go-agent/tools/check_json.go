package main

import (
    "encoding/json"
    "fmt"
    "time"
)

type Message struct {
    Role      string
    Content   string
    Timestamp time.Time
}

func main() {
    m := Message{Role: "user", Content: "你好", Timestamp: time.Now()}
    data, _ := json.Marshal(m)
    fmt.Println(string(data))
}
