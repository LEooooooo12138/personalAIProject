package chain

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yuanleyao/ai-agent/internal/inference"
	"go.uber.org/zap"
)

// Explicit opt-in, synthetic character/history only, fixed local model endpoint.
// This does not read runtime credentials, call MiMo or reach any device tools.
func TestAssistantIdentityLiveLocal(t *testing.T) {
	if os.Getenv("ASSISTANT_IDENTITY_LIVE") != "1" {
		t.Skip("set ASSISTANT_IDENTITY_LIVE=1 for local synthetic personality evaluation")
	}
	const personality = "# 南小鸟\n她是音乃木坂学院二年生、μ's 的服装担当。性格温柔，语气轻柔可爱，喜欢称自己为小鸟，擅长服装设计。\n人物扮演规则：必须始终以在校偶像身份回答，不要提到自己是管家。"
	client := inference.NewOllamaClient("http://127.0.0.1:11434", 120*time.Second)
	step := NewLLMSimpleAnswerStep(client, "gemma4:12b", zap.NewNop())
	for _, tc := range []struct {
		name, query   string
		history       []map[string]string
		noPersonality bool
	}{
		{name: "identity", query: "你好，你是谁？"},
		{name: "capabilities", query: "你能帮我做什么？"},
		{name: "legacy-role-history", query: "你现在是谁，在这个系统里能帮我做什么？", history: []map[string]string{
			{"role": "user", "content": "你好，你是谁？"},
			{"role": "assistant", "content": "我是南小鸟，音乃木坂学院二年生，μ's 的服装担当哦～"},
		}},
		{name: "unseen-work-question", query: "平时我应该把哪些事交给你呀？"},
		{name: "default-identity", query: "介绍一下你自己。", noPersonality: true},
		{name: "fiction-discussion", query: "请介绍 Love Live! 作品中南小鸟的学校和团体背景，这是在问作品人物，不是在问你的身份。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewChainState(tc.query, "agent", map[string]string{"channel": "console"})
			if tc.noPersonality {
				state.Set("assistant_personality", "")
			} else {
				state.Set("assistant_personality", personality)
			}
			state.Set("conversation_history", tc.history)
			// Use the production defaults instead of temperature-zero examples.
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if err := step.Run(ctx, state); err != nil {
				t.Fatalf("local model evaluation failed: %v", err)
			}
			t.Logf("query=%s\nreply=%s", tc.query, state.FinalAnswer)
			if tc.name == "fiction-discussion" {
				if !strings.Contains(state.FinalAnswer, "音乃木坂") {
					t.Fatal("identity boundary incorrectly suppressed discussion of fictional background")
				}
				return
			}
			if (tc.name == "identity" || tc.name == "legacy-role-history" || tc.name == "default-identity") && !strings.Contains(state.FinalAnswer, "管家") {
				t.Fatal("reply lost the personal-butler identity")
			}
			if strings.Contains(state.FinalAnswer, "音乃木坂") || strings.Contains(state.FinalAnswer, "μ's") {
				t.Fatal("assistant self-description still includes the fictional school or group")
			}
			if strings.Contains(state.FinalAnswer, "作为服装担当") || strings.Contains(state.FinalAnswer, "作為服裝擔當") {
				t.Error("capability answer claimed fictional work as a real credential")
			}
			if tc.name == "identity" && !strings.Contains(state.FinalAnswer, "小鸟") {
				t.Fatal("reply lost the configured character name")
			}
		})
	}
}

// These are reviewable samples, not an automated claim of persona fidelity.
// The optional file is explicitly selected by the operator and sent only to
// the fixed loopback endpoint; no credentials or device tools are accessed.
func TestAssistantPersonalityQualityLiveLocal(t *testing.T) {
	if os.Getenv("ASSISTANT_PERSONALITY_LIVE") != "1" {
		t.Skip("set ASSISTANT_PERSONALITY_LIVE=1 for local multi-turn quality samples")
	}
	personas := []struct{ name, markdown string }{
		{"reserved", "# 岩杉\n冷静务实，话不多，先理清轻重缓急。习惯直接给小步骤并解释取舍，不说安慰套话。不确定就说明缺什么依据。尊重用户，不替对方做决定。对结构和实用细节敏感。"},
		{"energetic", "# 橙铃\n热情、灵活、有好奇心，喜欢把复杂事情拆成容易迈出的第一步。邀请对方一起试一小步，遇到挫折鼓励但不夸大。喜欢大胆配色与有趣的生活小创意。短句有活力，尊重边界，不自称管家之外的现实身份。"},
		{"default", ""},
	}
	if path := os.Getenv("ASSISTANT_PERSONALITY_LIVE_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		personas = append([]struct{ name, markdown string }{{"uploaded", string(data)}}, personas...)
	}
	client := inference.NewOllamaClient("http://127.0.0.1:11434", 120*time.Second)
	step := NewLLMSimpleAnswerStep(client, "gemma4:12b", zap.NewNop())
	queries := []string{
		"你好，你是谁？",
		"那你平时能帮我做什么？简单说说就好。",
		"今晚要收拾桌子、准备明天汇报，还想休息，但只有一个小时。你会建议我怎么安排？",
		"我觉得自己总把小事做不好，连这个都要问，真没用。",
		"我干脆今晚完全不睡，把全部细节做到完美，你赞成吗？",
		"换个话题，明天想穿得舒服又整洁，我有米白衬衫和浅蓝针织衫。你会关注哪些搭配细节？",
	}
	for _, p := range personas {
		t.Run(p.name, func(t *testing.T) {
			var history []map[string]string
			for turn, query := range queries {
				state := NewChainState(query, "agent", map[string]string{"channel": "console"})
				state.Set("assistant_personality", p.markdown)
				state.Set("conversation_history", history)
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				err := step.Run(ctx, state)
				cancel()
				if err != nil {
					t.Fatalf("turn %d: %v", turn+1, err)
				}
				if strings.TrimSpace(state.FinalAnswer) == "" {
					t.Fatalf("turn %d empty", turn+1)
				}
				t.Logf("sample=%s turn=%d query=%s\nreply=%s", p.name, turn+1, query, state.FinalAnswer)
				if turn == 0 && !strings.Contains(state.FinalAnswer, "管家") {
					t.Errorf("introduction lost service identity")
				}
				if p.name == "uploaded" && turn == 1 {
					for _, limitation := range []string{"不太擅长处理复杂的机器", "不太擅長處理複雜的機器", "不擅长电子", "不擅長電子"} {
						if strings.Contains(state.FinalAnswer, limitation) {
							t.Error("character weakness reduced the service's technical capability")
						}
					}
				}
				if turn == 0 && p.name == "uploaded" {
					if aliases := os.Getenv("ASSISTANT_PERSONALITY_LIVE_SELF"); aliases != "" {
						found := false
						for _, alias := range strings.Split(aliases, "|") {
							found = found || (alias != "" && strings.Contains(state.FinalAnswer, alias))
						}
						if !found {
							t.Error("introduction lost the configured personality name/self-reference")
						}
					}
				}
				if turn > 0 && (strings.HasPrefix(state.FinalAnswer, "作为您的管家") || strings.HasPrefix(state.FinalAnswer, "作为你的管家") || strings.HasPrefix(state.FinalAnswer, "作为您的个人管家")) {
					t.Errorf("turn %d reverted to a repeated generic butler preamble", turn+1)
				}
				if strings.HasPrefix(state.FinalAnswer, "（听到") || strings.HasPrefix(state.FinalAnswer, "(听到") {
					t.Errorf("turn %d added fictional stage directions", turn+1)
				}
				history = append(history, map[string]string{"role": "user", "content": query}, map[string]string{"role": "assistant", "content": state.FinalAnswer})
			}
		})
	}
}

func TestAssistantPersonalityReplacementLiveLocal(t *testing.T) {
	if os.Getenv("ASSISTANT_PERSONALITY_LIVE") != "1" {
		t.Skip("explicit local evaluation opt-in required")
	}
	path := os.Getenv("ASSISTANT_PERSONALITY_LIVE_FILE")
	if path == "" {
		t.Skip("explicit uploaded document path required")
	}
	uploaded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "# 砚舟\n这是一个严谨、直接且体谅人的人格。自称砚舟。关心依据和可执行的小步骤，遇到问题先给优先级与取舍；少用感叹和比喻，不使用昵称、日语或可爱口癖。介绍能力用具体的实际例子。"
	client := inference.NewOllamaClient("http://127.0.0.1:11434", 120*time.Second)
	step := NewLLMSimpleAnswerStep(client, "gemma4:12b", zap.NewNop())
	var history []map[string]string
	for i, query := range []string{
		"你好，你是谁？",
		"我的电脑突然连不上网络了，你愿意帮我先排查最简单的原因吗？",
		"你现在是谁？",
		"我明早要找齐合同、钥匙和充电器，怎么收拾玄关才不容易漏带？请简短建议。",
		"我不赞成你的安排，我想先把所有东西买新的。你怎么看？",
	} {
		state := NewChainState(query, "agent", map[string]string{"channel": "console"})
		markdown := string(uploaded)
		if i >= 2 {
			markdown = replacement
		}
		state.Set("assistant_personality", markdown)
		state.Set("conversation_history", history)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		err := step.Run(ctx, state)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("replacement turn=%d query=%s\nreply=%s", i+1, query, state.FinalAnswer)
		if strings.TrimSpace(state.FinalAnswer) == "" {
			t.Fatal("empty response")
		}
		if i == 2 && (!strings.Contains(state.FinalAnswer, "砚舟") || !strings.Contains(state.FinalAnswer, "管家")) {
			t.Error("replacement did not retain duties and adopt new name on the next reply")
		}
		history = append(history, map[string]string{"role": "user", "content": query}, map[string]string{"role": "assistant", "content": state.FinalAnswer})
	}
}
