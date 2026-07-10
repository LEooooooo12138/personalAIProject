package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\internal\gateway\websocket.go`
    data, _ := os.ReadFile(path)
    text := string(data)

    // Pattern for handleChat: the second user AddMessage with exact tabs
    old := "w.sessionMgr.AddMessage(session, core.Message{\n\t\t\tRole: \"user\", Content: content, Timestamp: time.Now(),\n\t\t})\n\t}\n\n\tif w.chainExecutor != nil && w.chainRouter != nil {"
    repl := "w.sessionMgr.AddMessage(session, core.Message{\n\t\t\tRole: \"user\", Content: content, Timestamp: time.Now(),\n\t\t})\n\n\t\t// Index user message for semantic session search.\n\t\tif w.sessionStore != nil {\n\t\t\tmsgIdx := len(session.Messages) - 1\n\t\t\tgo w.sessionStore.IndexMessage(context.Background(), session, msgIdx, core.Message{Role: \"user\", Content: content, Timestamp: time.Now()})\n\t\t}\n\t}\n\n\tif w.chainExecutor != nil && w.chainRouter != nil {"

    if strings.Contains(text, old) {
        text = strings.Replace(text, old, repl, 1)
        fmt.Println("OK: Added IndexMessage call in handleChat")
    } else {
        // Try with 2 tabs for Role line
        old2 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"user\", Content: content, Timestamp: time.Now(),\n\t})\n\t}\n\n\tif w.chainExecutor != nil"
        if strings.Contains(text, old2) {
            fmt.Println("Found pattern with 2 tabs, but already handled by previous run?")
        }
        fmt.Println("FAIL: pattern not found")
        // Debug: find the exact context
        idx := strings.Index(text, "w.sessionMgr.AddMessage(session, core.Message{")
        if idx >= 0 {
            snippet := text[idx:idx+200]
            fmt.Printf("Context at AddMessage:\n---\n%s\n---\n", snippet)
        }
        os.Exit(1)
    }

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Done")
}
