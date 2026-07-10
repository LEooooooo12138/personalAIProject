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

    funcStart := "func (w *wsConn) handleChatWithChain(content string, session *core.Session) {"
    funcEnd := "\nfunc (w *wsConn) handleChatLegacy"

    startIdx := strings.Index(text, funcStart)
    endIdx := strings.Index(text, funcEnd)
    if startIdx < 0 || endIdx < 0 {
        fmt.Println("ERROR: function boundaries not found")
        fmt.Printf("start=%d end=%d\n", startIdx, endIdx)
        os.Exit(1)
    }

    newFunc := `func (w *wsConn) handleChatWithChain(content string, session *core.Session) {
	history := buildHistoryAsMap(session)
	metadata := map[string]string{"channel": "webchat"}
	state := chain.NewChainState(content, "personal", metadata)
	state.Data["conversation_history"] = history

	streamCh := make(chan chain.StreamToken, 50)
	state.Stream = streamCh
	state.StreamMode = true

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	go func() {
		result, err := w.chainExecutor.Run(ctx, "chat", state)
		if err != nil {
			w.logger.Error("chain async error", zap.Error(err))
		} else if result != nil && result.Error != nil {
			w.logger.Error("chain async step error", zap.Error(result.Error))
		}
		for _, t := range state.Trace {
			w.logger.Debug("chain step trace",
				zap.String("step", t.StepName),
				zap.Duration("duration", t.Duration),
				zap.String("status", t.Status),
			)
		}
		close(streamCh)
	}()

	var fullResponse strings.Builder
	for token := range streamCh {
		switch {
		case token.Sources != nil:
			var srcList []string
			for _, s := range token.Sources {
				srcList = append(srcList, s.Title)
			}
			w.writeJSON(serverMessage{Type: "context", Content: "\u68c0\u7d22\u5230\u76f8\u5173\u9875\u9762: " + strings.Join(srcList, ", ")})
		case token.Error != "":
			w.writeJSON(serverMessage{Type: "error", Message: token.Error})
		case token.Text != "":
			fullResponse.WriteString(token.Text)
			w.writeJSON(serverMessage{Type: "stream", Content: token.Text})
		}
	}

	responseText := fullResponse.String()
	if responseText == "" {
		responseText = state.FinalAnswer
	}
	if responseText == "" {
		sourceCount, _ := state.Get("source_count")
		mergedCount, _ := state.Get("merged_count")
		sysPrompt, _ := state.GetString("system_prompt")
		sysPreview := ""
		if len(sysPrompt) > 200 {
			sysPreview = sysPrompt[:200] + "..."
		} else {
			sysPreview = sysPrompt
		}
		debugRaw, _ := state.GetString("_llm_raw_response")
		errStr := ""
		if llmErr, ok := state.GetString("_llm_error"); ok && llmErr != "" {
			errStr += "\nllm error: " + llmErr
		}
		responseText = "\u62b1\u6b49\uff0c\u6211\u6682\u65f6\u65e0\u6cd5\u56de\u7b54\u8fd9\u4e2a\u95ee\u9898\u3002\n\n[debug] sources=" + fmt.Sprintf("%v", sourceCount) + " merged=" + fmt.Sprintf("%v", mergedCount) + " sys_prompt_len=" + fmt.Sprintf("%d", len(sysPrompt)) + " llm_raw_len=" + fmt.Sprintf("%d", len(debugRaw)) + errStr + "\nsys_prompt_preview: " + sysPreview
	}

	metadata["platform"] = "webchat"
	filtered, _ := w.filter.Apply(responseText, metadata)

	w.sessionMgr.AddMessage(session, core.Message{
		Role: "assistant", Content: filtered, Timestamp: time.Now(),
	})

	if w.sessionStore != nil {
		go func() {
			if err := w.sessionStore.SaveSession(w.session); err != nil {
				w.logger.Warn("session save failed", zap.Error(err))
			}
		}()
	}

	w.writeJSON(serverMessage{Type: "response", Content: filtered})
}
`

    text = text[:startIdx] + newFunc + text[endIdx:]

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("OK: streaming support added to websocket.go")
}
