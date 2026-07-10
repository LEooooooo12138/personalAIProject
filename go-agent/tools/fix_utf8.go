package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\cmd\agentd\main.go`
    data, _ := os.ReadFile(path)
    text := string(data)

    // Fix: "bridges gateway.EmbeddingStore → chain.EmbeddingSearcher"
    // The arrow → (U+2192) got corrupted; find the broken pattern
    prefix := "// Create adapter that bridges gateway.EmbeddingStore "
    idx := strings.Index(text, prefix)
    if idx >= 0 {
        nl := strings.Index(text[idx:], "\n")
        if nl > 0 {
            oldLine := text[idx : idx+nl]
            newLine := prefix + "\u2192 chain.EmbeddingSearcher"
            text = strings.Replace(text, oldLine, newLine, 1)
            fmt.Println("Fixed arrow comment in main.go")
        }
    }

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Done")
}
