package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	data, err := os.ReadFile(`E:\personalAIProject\go-agent\internal\chain\llm_steps.go`)
	if err != nil {
		fmt.Println("read error:", err)
		os.Exit(1)
	}
	content := string(data)

	thinking := "\t\t\"thinking\":    map[string]string{\"type\": \"disabled\"},\n"

	for _, n := range []string{"4096", "2048", "1024"} {
		old := fmt.Sprintf("\"max_tokens\":  %s,\n\t})", n)
		new := fmt.Sprintf("\"max_tokens\":  %s,\n%s\t})", n, thinking)
		content = strings.ReplaceAll(content, old, new)

		old2 := fmt.Sprintf("\"max_tokens\":  %s,\n\t\t})", n)
		new2 := fmt.Sprintf("\"max_tokens\":  %s,\n%s\t\t})", n, thinking)
		content = strings.ReplaceAll(content, old2, new2)
	}

	err = os.WriteFile(`E:\personalAIProject\go-agent\internal\chain\llm_steps.go`, []byte(content), 0644)
	if err != nil {
		fmt.Println("write error:", err)
		os.Exit(1)
	}
	fmt.Println("OK - thinking disabled")
}
