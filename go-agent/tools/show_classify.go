package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\internal\chain\go_steps.go`
    data, _ := os.ReadFile(path)
    text := string(data)
    
    // Show current classification function
    idx := strings.Index(text, "func (s *QueryClassifyStep) Run")
    if idx < 0 {
        fmt.Println("not found")
        os.Exit(1)
    }
    
    endIdx := strings.Index(text[idx:], "func (s *QueryClassifyStep) Decide")
    if endIdx < 0 {
        fmt.Println("decide not found")
        os.Exit(1)
    }
    
    fmt.Println(text[idx:idx+endIdx])
}
