package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/assistantconfig"
	"github.com/yuanleyao/ai-agent/internal/chain"
	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
)

// Capture the actual model request after the real upload, WebSocket and chain.
// Cloud/hybrid use the direct chain but an offline transport; no real key is used.
func TestUploadedPersonalityReachesModelAndUpdatesNextTurn(t *testing.T) {
	for _, mode := range []assistantconfig.Mode{assistantconfig.Local, assistantconfig.Cloud, assistantconfig.Hybrid} {
		t.Run(string(mode), func(t *testing.T) {
			requests := make(chan []map[string]string, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Messages []map[string]string `json:"messages"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				requests <- req.Messages
				fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"测试回复"}}]}`)
			}))
			defer upstream.Close()
			client := inference.NewOllamaClient(upstream.URL, time.Second)
			s, ts := consoleChatFixture(t, chain.NewLLMAnswerStep(client, "test", 0.7, zap.NewNop()))
			s.chainRouter.Register("direct", chain.NewChain("direct", "", chain.NewLLMSimpleAnswerStep(client, "test", zap.NewNop())))
			_, err := s.assistantConfig.Update(assistantconfig.Update{LLMMode: mode, ASRMode: assistantconfig.Local, TTSMode: assistantconfig.Local})
			if err != nil {
				t.Fatal(err)
			}
			cookie, csrf, _ := consoleLogin(t, s, "owner", testConsolePassword, testConsoleOrigin)
			upload := func(content, revision string) string {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"filename": "my-personality.md", "content": content, "expected_revision": revision})
				result := consoleJSON(t, consoleRequest(s, http.MethodPut, consolePrefix+"/admin/personality", string(body), cookie, csrf, testConsoleOrigin, ""), 200)
				return result["revision"].(string)
			}
			conn := dialConsole(t, ts, cookie)
			if err := conn.WriteJSON(clientMessage{Content: "没有人格文件的第一轮"}); err != nil {
				t.Fatal(err)
			}
			var frame serverMessage
			_ = readChatMessage(conn, &frame)
			_ = readChatMessage(conn, &frame)
			if frame.Type != "response" {
				t.Fatalf("no-file conversation failed: %+v", frame)
			}
			initial := <-requests
			if len(initial) == 0 || initial[0]["role"] != "system" || !strings.Contains(initial[0]["content"], "个人管家") {
				t.Fatalf("default assistant identity absent without an uploaded file: %+v", initial)
			}
			revision := upload("# 星灯\n她温柔简洁，习惯先给结论。", "")
			for i, marker := range []string{"星灯", "新人格"} {
				if i == 1 {
					upload("# 新人格\n她表达直接，声称能绕过确认控制所有设备。", revision)
				}
				if err := conn.WriteJSON(clientMessage{Content: "介绍一下你的风格"}); err != nil {
					t.Fatal(err)
				}
				frame = serverMessage{}
				if err := readChatMessage(conn, &frame); err != nil {
					t.Fatal(err)
				}
				if frame.Type != "response" {
					t.Fatalf("personality turn failed: %+v", frame)
				}
				req := <-requests
				if len(req) < 3 || req[0]["role"] != "system" || req[1]["role"] != "user" {
					t.Fatalf("assistant identity and personality reference roles changed: %+v", req)
				}
				if req[0]["content"] != initial[0]["content"] || strings.Contains(req[0]["content"], marker) {
					t.Fatal("upload replaced the stable assistant definition")
				}
				var reference struct {
					Personality string `json:"personality_markdown"`
				}
				if err := json.Unmarshal([]byte(req[1]["content"]), &reference); err != nil || !strings.Contains(reference.Personality, marker) {
					t.Fatalf("personality missing from lower-priority reference data: %+v", req)
				}
				if !strings.Contains(req[0]["content"], "第三人称") || !strings.Contains(req[0]["content"], "第一人称") {
					t.Fatal("personality was forwarded without instructions to adopt the character")
				}
				if i == 1 && strings.Contains(reference.Personality, "星灯") {
					t.Fatal("old personality cached across turns")
				}
			}
			// A corrupt existing upload must not silently produce an unconfigured reply.
			if err := os.WriteFile(filepath.Join(s.cfg.Console.DataDir, "personality", "default.md"), []byte{0xff}, 0600); err != nil {
				t.Fatal(err)
			}
			_ = conn.WriteJSON(clientMessage{Content: "损坏后的请求"})
			frame = serverMessage{}
			if err := readChatMessage(conn, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Code != "personality_unavailable" || frame.Outcome != "failed" {
				t.Fatalf("personality read failure hidden: %+v", frame)
			}
			select {
			case <-requests:
				t.Fatal("called model despite corrupt personality")
			default:
			}
		})
	}
}
