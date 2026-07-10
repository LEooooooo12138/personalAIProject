package main

import (
    "fmt"
    "os"
    "strings"
)

func main() {
    path := "E:\\personalAIProject\\go-agent\\internal\\chain\\chains.go"
    data, _ := os.ReadFile(path)
    text := string(data)

    funcStart := "func BuildRAGChatChain("
    anchor := "Wiki Ingest Chain"

    startIdx := strings.Index(text, funcStart)
    anchorIdx := strings.Index(text, anchor)
    if startIdx < 0 || anchorIdx < 0 {
        fmt.Println("ERROR: boundaries not found")
        os.Exit(1)
    }

    prevBlank := strings.LastIndex(text[:anchorIdx], "\n\n")
    if prevBlank > 0 { anchorIdx = prevBlank + 1 }

    newFunc := "func BuildRAGChatChain(\n\tvr vault.Reader,\n\tinfer inference.Client,\n\tmodel string,\n\tembedStore EmbeddingSearcher,\n\tlogger *zap.Logger,\n) (*ChainRouter, error) {\n\n\trouter := NewChainRouter()\n\n\tmainChain := NewChain(\n\t\t\"chat\",\n\t\t\"LLM自主决策是否搜索知识库\",\n\t\tNewLLMDecideAndAnswerStep(infer, model, logger),\n\t\tNewVaultSearchStep(vr, embedStore, 5, logger),\n\t\tNewPagePreprocessStep(1000),\n\t\tNewContextAssemblyStep(5),\n\t\tNewLLMAnswerStep(infer, model, 0.7, logger),\n\t)\n\trouter.Register(\"chat\", mainChain)\n\n\tragChain := NewChain(\n\t\t\"rag-answer\",\n\t\t\"强制检索知识库 + LLM 回答\",\n\t\tNewVaultSearchStep(vr, embedStore, 5, logger),\n\t\tNewPagePreprocessStep(1000),\n\t\tNewContextAssemblyStep(5),\n\t\tNewLLMAnswerStep(infer, model, 0.7, logger),\n\t)\n\trouter.Register(\"rag-answer\", ragChain)\n\n\treturn router, nil\n}\n"

    text = text[:startIdx] + newFunc + text[anchorIdx:]
    os.WriteFile(path, []byte(text), 0644)
    fmt.Println("Step 2 done")
}
