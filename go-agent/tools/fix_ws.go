package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\internal\gateway\websocket.go`
    data, err := os.ReadFile(path)
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    text := string(data)
    changes := 0

    // 1. In handleChat: add IndexMessage after second user AddMessage
    old2 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"user\", Content: content, Timestamp: time.Now(),\n\t})\n\t}\n\n\tif w.chainExecutor != nil && w.chainRouter != nil {"
    repl2 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"user\", Content: content, Timestamp: time.Now(),\n\t})\n\n\t\t// Index user message for semantic session search.\n\t\tif w.sessionStore != nil {\n\t\t\tmsgIdx := len(session.Messages) - 1\n\t\t\tgo w.sessionStore.IndexMessage(context.Background(), session, msgIdx, core.Message{Role: \"user\", Content: content, Timestamp: time.Now()})\n\t\t}\n\t}\n\n\tif w.chainExecutor != nil && w.chainRouter != nil {"
    if strings.Contains(text, old2) {
        text = strings.Replace(text, old2, repl2, 1)
        changes++
        fmt.Println("[1/3] Added IndexMessage call in handleChat")
    }

    // 2. handleChatWithChain: add SaveSession after assistant AddMessage
    old3 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"assistant\", Content: filtered, Timestamp: time.Now(),\n\t})\n\n\tif result != nil {"
    repl3 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"assistant\", Content: filtered, Timestamp: time.Now(),\n\t})\n\n\t// Persist session to disk (survives restart).\n\tif w.sessionStore != nil {\n\t\tgo func() {\n\t\t\tif err := w.sessionStore.SaveSession(w.session); err != nil {\n\t\t\t\tw.logger.Warn(\"session save failed\", zap.Error(err))\n\t\t\t}\n\t\t}()\n\t}\n\n\tif result != nil {"
    if strings.Contains(text, old3) {
        text = strings.Replace(text, old3, repl3, 1)
        changes++
        fmt.Println("[2/3] Added SaveSession call in handleChatWithChain")
    }

    // 3. handleChatLegacy: add SaveSession after assistant AddMessage
    old4 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"assistant\", Content: filtered, Timestamp: time.Now(),\n\t})\n\n\tif len(sources) > 0 {"
    repl4 := "w.sessionMgr.AddMessage(session, core.Message{\n\t\tRole: \"assistant\", Content: filtered, Timestamp: time.Now(),\n\t})\n\n\t// Persist session to disk (survives restart).\n\tif w.sessionStore != nil {\n\t\tgo func() {\n\t\t\tif err := w.sessionStore.SaveSession(w.session); err != nil {\n\t\t\t\tw.logger.Warn(\"session save failed\", zap.Error(err))\n\t\t\t}\n\t\t}()\n\t}\n\n\tif len(sources) > 0 {"
    if strings.Contains(text, old4) {
        text = strings.Replace(text, old4, repl4, 1)
        changes++
        fmt.Println("[3/3] Added SaveSession call in handleChatLegacy")
    }

    if changes == 0 {
        fmt.Println("ERROR: no pattern matched")
        os.Exit(1)
    }

    if err := os.WriteFile(path, []byte(text), 0644); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    fmt.Printf("Done: %d changes applied\n", changes)
}
