package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := `E:\personalAIProject\go-agent\static\chat-widget.js`
    data, _ := os.ReadFile(path)
    text := string(data)

    // Find the ws.onmessage handler and add stream handling
    // Current pattern: data.type === "response" / "error" 
    // We need to add "stream" handling that creates/updates an incremental message div

    // Find the response handler to insert stream handler before it
    responsePattern := `} else if (data.type === "response") {`
    streamHandler := `} else if (data.type === "stream") {
        // Incremental streaming token: build text character by character.
        removeTyping();
        var streamEl = document.getElementById("cw-stream-msg");
        if (!streamEl) {
            streamEl = document.createElement("div");
            streamEl.className = "cw-msg assistant";
            streamEl.id = "cw-stream-msg";
            messages.appendChild(streamEl);
        }
        // Accumulate raw text and re-render as Markdown each token.
        streamEl._rawText = (streamEl._rawText || "") + data.content;
        streamEl.innerHTML = renderMd(streamEl._rawText);
        messages.scrollTop = messages.scrollHeight;
    } else if (data.type === "response") {
        // Final response: clear any streaming state and display the complete message.
        var streamMsg = document.getElementById("cw-stream-msg");
        if (streamMsg) {
            streamMsg.removeAttribute("id");
            streamMsg._rawText = null;
        } else {
            appendMessage("assistant", data.content);
        }
    }`

    text = strings.Replace(text, responsePattern, streamHandler, 1)

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("OK: stream handler added to chat-widget.js")
}
