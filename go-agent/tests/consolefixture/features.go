package main

import (
	"context"
	"encoding/json"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/core"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
	"github.com/yuanleyao/ai-agent/internal/vault"
	"strings"
	"time"
)

// This model exists only in the disposable browser fixture. Real chain retrieval,
// source construction and wiki write/index steps still execute against temp dirs.
type fixtureKnowledgeModel struct{}

func (fixtureKnowledgeModel) Chat(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if e := json.Unmarshal(request, &req); e != nil {
		return nil, e
	}
	joined := ""
	for _, m := range req.Messages {
		joined += m.Content + "\n"
	}
	answer := "离线测试：未找到匹配资料。"
	if strings.Contains(joined, "P42") {
		answer = "离线私人回答：紫藤私人物品编号 P42。"
	}
	if strings.Contains(joined, "knowledge management expert") {
		answer = "---\ntitle: 离线导入笔记\ncategory: concepts\ntags: [fixture, rag]\n---\n\n# 离线导入笔记\n\n紫藤导入内容，仅保存在私人知识库。"
	}
	return json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}}}})
}
func (fixtureKnowledgeModel) Embed(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errFixtureOffline
}
func (fixtureKnowledgeModel) ListModels(context.Context) ([]inference.ModelInfo, error) {
	return nil, errFixtureOffline
}

func prepareFixtureFeatures(app *core.App, router *chain.ChainRouter) error {
	ctx := context.Background()
	pages := []struct{ scope, path, title, body string }{
		{"agent", "concepts/public-guide.md", "家庭公开指南", "紫藤放在书房窗边。此页为公开资料。"},
		{"personal", "concepts/private-note.md", "管理员私人笔记", "紫藤私人物品编号 P42，仅供管理员阅读。"},
	}
	for _, p := range pages {
		content := "---\ntitle: " + p.title + "\ncategory: concepts\ntags: [fixture, rag]\n---\n\n# " + p.title + "\n\n" + p.body
		if e := app.VaultW.WritePage(ctx, p.scope, p.path, []byte(content)); e != nil {
			return e
		}
		if writer, ok := app.VaultW.(vault.IndexWriter); ok {
			if e := writer.UpdateIndex(ctx, p.scope, vault.IndexEntry{Path: p.path, Title: p.title}); e != nil {
				return e
			}
		}
	}
	all, e := chain.BuildAllChains(chain.ChainDeps{VaultReader: app.VaultR, VaultWriter: app.VaultW, Infer: fixtureKnowledgeModel{}, Model: "fixture", PersonalPath: app.Config.Vaults.Personal, AgentPath: app.Config.Vaults.Agent, Logger: app.Logger})
	if e != nil {
		return e
	}
	for _, name := range []string{"rag-answer", "wiki-ingest"} {
		c, e := all.Route(name)
		if e != nil {
			return e
		}
		router.Register(name, c)
	}
	return app.SmartHome.GetStore().SaveSuggestions([]smarthome.RuleSuggestion{{ID: "fixture_time", CreatedAt: time.Now().UTC(), Confidence: .9, Intent: &smarthome.SuggestionIntent{SchemaVersion: 1, Kind: "time", EntityID: "switch.channel_1", At: "18:00:00", TimeZone: "Asia/Shanghai", RequiredConditions: []string{"presence_home"}}, MissingBindings: []string{"presence_home"}}})
}
