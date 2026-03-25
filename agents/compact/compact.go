package compact

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3/responses"
)

const (
	DefaultKeepRecentToolResults  = 4
	DefaultAutoCompactCharLimit   = 200_000
	DefaultAutoCompactKeepRecentK = 12
)

// ApproxChars returns an approximate transcript size in characters.
func ApproxChars(items []responses.ResponseInputItemUnionParam) int {
	total := 0
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		total += len(encoded)
	}
	return total
}

// NeedsAutoCompact reports whether current items exceed the configured limit.
func NeedsAutoCompact(items []responses.ResponseInputItemUnionParam, charLimit int) bool {
	if charLimit <= 0 {
		return false
	}
	return ApproxChars(items) > charLimit
}

// MicroCompact keeps recent tool results and replaces older ones with placeholders.
func MicroCompact(items []responses.ResponseInputItemUnionParam, keepRecent int) ([]responses.ResponseInputItemUnionParam, int) {
	if keepRecent < 0 {
		keepRecent = 0
	}

	toolByCallID := buildToolByCallID(items)
	toolResultIndexes := findToolResultIndexes(items)
	if len(toolResultIndexes) <= keepRecent {
		return append([]responses.ResponseInputItemUnionParam{}, items...), 0
	}

	next := append([]responses.ResponseInputItemUnionParam{}, items...)
	compactUntil := len(toolResultIndexes) - keepRecent
	for _, index := range toolResultIndexes[:compactUntil] {
		item := items[index]
		if item.OfFunctionCallOutput == nil {
			continue
		}
		callID := strings.TrimSpace(item.OfFunctionCallOutput.CallID)
		toolName := toolByCallID[callID]
		if toolName == "" {
			toolName = "unknown"
		}
		placeholder := fmt.Sprintf("[tool_result compacted: tool=%s]", toolName)
		next[index] = responses.ResponseInputItemParamOfFunctionCallOutput(callID, placeholder)
	}
	return next, compactUntil
}

// AutoCompact keeps instruction messages + recent items and injects summary.
func AutoCompact(items []responses.ResponseInputItemUnionParam, summary string, keepRecentItems int) []responses.ResponseInputItemUnionParam {
	if keepRecentItems < 0 {
		keepRecentItems = 0
	}
	if len(items) == 0 {
		return nil
	}

	keepIndexes := make(map[int]struct{}, len(items))
	for index, item := range items {
		if isInstructionMessage(item) {
			keepIndexes[index] = struct{}{}
		}
	}
	start := len(items) - keepRecentItems
	if start < 0 {
		start = 0
	}
	for index := start; index < len(items); index++ {
		keepIndexes[index] = struct{}{}
	}

	result := make([]responses.ResponseInputItemUnionParam, 0, len(keepIndexes)+1)
	for index, item := range items {
		if _, ok := keepIndexes[index]; ok {
			result = append(result, item)
		}
	}

	trimmedSummary := strings.TrimSpace(summary)
	if trimmedSummary == "" {
		return result
	}
	// 摘要作为最新 developer 指令注入，不把旧长历史继续携带。
	result = append(result, responses.ResponseInputItemParamOfMessage(
		"Conversation summary (auto-compact):\n"+trimmedSummary,
		responses.EasyInputMessageRoleDeveloper,
	))
	return result
}

func buildToolByCallID(items []responses.ResponseInputItemUnionParam) map[string]string {
	result := make(map[string]string)
	for _, item := range items {
		if item.OfFunctionCall == nil {
			continue
		}
		callID := strings.TrimSpace(item.OfFunctionCall.CallID)
		if callID == "" {
			continue
		}
		result[callID] = strings.TrimSpace(item.OfFunctionCall.Name)
	}
	return result
}

func findToolResultIndexes(items []responses.ResponseInputItemUnionParam) []int {
	indexes := make([]int, 0)
	for index, item := range items {
		if item.OfFunctionCallOutput == nil {
			continue
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func isInstructionMessage(item responses.ResponseInputItemUnionParam) bool {
	if item.OfMessage != nil {
		return item.OfMessage.Role == responses.EasyInputMessageRoleDeveloper ||
			item.OfMessage.Role == responses.EasyInputMessageRoleSystem
	}
	if item.OfInputMessage != nil {
		role := strings.ToLower(strings.TrimSpace(item.OfInputMessage.Role))
		return role == "developer" || role == "system"
	}
	return false
}
