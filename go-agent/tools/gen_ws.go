package main

import "os"

func main() {
	content := "package gateway\n\nimport (\n\t\"context\"\n\t\"crypto/rand\"\n\t\"encoding/hex\"\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"net/http\"\n\t\"strings\"\n\t\"sync\"\n\t\"time\"\n\n\t\"github.com/gorilla/websocket\"\n\t\"go.uber.org/zap\"\n\n\t\"github.com/yuanleyao/ai-agent/internal/chain\"\n\t\"github.com/yuanleyao/ai-agent/internal/core\"\n\t\"github.com/yuanleyao/ai-agent/internal/filter\"\n\t\"github.com/yuanleyao/ai-agent/internal/inference\"\n\t\"github.com/yuanleyao/ai-agent/internal/vault\"\n)\n\n"
	os.WriteFile("E:\\personalAIProject\\go-agent\\internal\\gateway\\websocket.go", []byte(content), 0644)
}