package chain

import "errors"

var ErrContextBudgetExceeded = errors.New("context_budget_exceeded")

// Nonpositive limits disable that limit. History limits count messages, but
// removal always happens in complete adjacent user/assistant pairs.
type ChatBudget struct {
	MaxInputBytes      int
	MaxHistoryMessages int
}

type BudgetResult struct {
	History      []map[string]string
	DroppedPairs int
	// InputBytes is the sum of UTF-8 content bytes, not serialized request bytes
	// (JSON escaping, roles/options/envelopes) and never a provider token count.
	InputBytes int
}

// ApplyChatBudget preserves required strings and retained messages verbatim.
// Callers must privacy-filter history first. Non-pair fragments are excluded,
// not counted as budget omissions; only eligible complete pairs can be dropped.
// system/personality accept the already assembled required message contents,
// including reference wrappers and separators that will be sent to the model.
func ApplyChatBudget(system, personality, current string, history []map[string]string, budget ChatBudget) (BudgetResult, error) {
	result := BudgetResult{InputBytes: len(system) + len(personality) + len(current)}
	if budget.MaxInputBytes > 0 && result.InputBytes > budget.MaxInputBytes {
		return result, ErrContextBudgetExceeded
	}
	for i := 0; i+1 < len(history); i++ {
		if history[i]["role"] != "user" || history[i+1]["role"] != "assistant" {
			continue
		}
		result.History = append(result.History, history[i], history[i+1])
		result.InputBytes += len(history[i]["content"]) + len(history[i+1]["content"])
		i++
	}
	for len(result.History) > 0 && ((budget.MaxHistoryMessages > 0 && len(result.History) > budget.MaxHistoryMessages) || (budget.MaxInputBytes > 0 && result.InputBytes > budget.MaxInputBytes)) {
		result.InputBytes -= len(result.History[0]["content"]) + len(result.History[1]["content"])
		result.History = result.History[2:]
		result.DroppedPairs++
	}
	return result, nil
}
