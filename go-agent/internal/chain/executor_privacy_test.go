package chain

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"strings"
	"testing"
)

// Removing the private-vault guard exposes inputs in execution diagnostics.
func TestExecutorPersonalQueryIsNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router := NewChainRouter()
	router.Register("rag-answer", NewChain("rag-answer", "", NewFuncStep("answer", func(_ context.Context, s *ChainState) error { s.FinalAnswer = "fixture answer"; return nil })))
	result, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "rag-answer", NewChainState("PRIVATE_INPUT_ONLY_485", "personal", nil))
	if err != nil || result.Error != nil || result.FinalAnswer != "fixture answer" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	if strings.Contains(fmt.Sprint(logs.All()), "PRIVATE_INPUT_ONLY_485") {
		t.Fatal("private input exposed in Info logs")
	}
	if logs.Len() == 0 {
		t.Fatal("execution diagnostics removed instead of private input")
	}
}

// Empty Vault is the existing pipeline's default personal Vault.
func TestExecutorDefaultPersonalQueryIsNotLogged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	router := NewChainRouter()
	router.Register("rag-answer", NewChain("rag-answer", "", NewFuncStep("answer", func(_ context.Context, s *ChainState) error { s.FinalAnswer = "fixture answer"; return nil })))
	_, err := NewChainExecutor(zap.New(core), router).Run(context.Background(), "rag-answer", NewChainState("DEFAULT_PRIVATE_INPUT_485", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(logs.All()), "DEFAULT_PRIVATE_INPUT_485") {
		t.Fatal("default personal input exposed in logs")
	}
}
