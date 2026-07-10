package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := "E:\\personalAIProject\\go-agent\\internal\\chain\\go_steps_decide.go"
    data, _ := os.ReadFile(path)
    text := string(data)

    // Find the systemPrompt line and replace it
    marker := "systemPrompt := "
    idx := strings.Index(text, marker)
    if idx < 0 { fmt.Println("not found"); os.Exit(1) }

    // Find end of the string literal (the closing ")
    strStart := idx + len(marker) + 1 // skip past the opening "
    strEnd := strings.Index(text[strStart:], `"`)
    if strEnd < 0 { fmt.Println("end not found"); os.Exit(1) }
    strEnd += strStart

    // New concise prompt
    newPrompt := `\u4f60\u662f\u7528\u6237\u7684\u4e2a\u4ebaAI\u52a9\u624b\u3002\u5982\u679c\u95ee\u9898\u6d89\u53ca\u7528\u6237\u7684\u4e2a\u4eba\u4fe1\u606f\uff08\u4eba\u7269\u3001\u7ecf\u5386\u3001\u9879\u76ee\u3001\u6280\u80fd\u3001\u6559\u80b2\u3001\u6e38\u620f\u7b49\uff09\uff0c\u53ea\u56de\u590d [SEARCH: \u5173\u952e\u8bcd]\u3002\u5176\u4ed6\u95ee\u9898\u76f4\u63a5\u56de\u7b54\u3002`

    text = text[:strStart] + newPrompt + text[strEnd:]

    // Also reduce max_tokens
    text = strings.Replace(text, `"max_tokens":  1024,`, `"max_tokens":  256,`, 1)

    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Prompt simplified, max_tokens reduced to 256")
}
