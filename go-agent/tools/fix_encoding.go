package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	files := []string{
		"internal/core/session_store.go",
		"internal/gateway/http.go",
		"internal/gateway/websocket.go",
		"cmd/agentd/main.go",
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("ERROR %s: %v\n", path, err)
			continue
		}
		// Fix all corrupted UTF-8: E2 86 3F -> E2 86 92 (→) and E2 80 3F -> E2 80 94 (—)
		data = bytes.ReplaceAll(data, []byte{0xE2, 0x86, 0x3F}, []byte{0xE2, 0x86, 0x92})
		data = bytes.ReplaceAll(data, []byte{0xE2, 0x80, 0x3F}, []byte{0xE2, 0x80, 0x94})
		// Fix E2 94 80 3F pattern (─?)
		data = bytes.ReplaceAll(data, []byte{0xE2, 0x94, 0x80, 0x3F}, []byte{0xE2, 0x94, 0x80, 0xE2})
		// Fix E2 94 82 3F pattern  
		data = bytes.ReplaceAll(data, []byte{0xE2, 0x94, 0x82, 0x3F}, []byte{0xE2, 0x94, 0x82, 0xE2})

		if err := os.WriteFile(path, data, 0644); err != nil {
			fmt.Printf("ERROR writing %s: %v\n", path, err)
			continue
		}
		fmt.Printf("OK: %s\n", path)
	}
}
