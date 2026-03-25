package runtime

import (
	"bufio"
	"context"
	"fmt"
	"nanocc/agents/compact"
	"nanocc/agents/skills"
	"nanocc/agents/subagent"
	"os"
	"strings"
	"time"

	"nanocc/internal/common"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

var (
	// 运行时的核心约束：要求模型通过 todo_set 维护任务状态。
	developerMessage = "You are a coding agent. Use tools `bash`, `read_file`, `write_file`, and `todo_set` when needed. You can manage skills with `skill_list`, `skill_load`, and `skill_unload`. Use `todo_set` only for non-trivial multi-step tasks (for example: code changes, file edits, debugging, or tasks requiring multiple actions). For simple single-turn Q&A, reply directly without creating TODO. If a TODO is started, keep it updated and reply directly once completed."
)

func RunInteractive() error {
	cfg := common.LoadConfig()
	if cfg.APIKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is empty")
	}

	client := common.NewClient(cfg)
	skillRegistry, err := skills.LoadRegistryFromDir(".skills")
	if err != nil {
		fmt.Printf("warning: failed to load .skills: %v\n", err)
		skillRegistry = skills.NewRegistry()
	}
	parentSkills := skills.NewState()

	todo := newTodoStateStore()
	subAgentMgr := subagent.NewManager(4)
	subAgentRunner := func(ctx context.Context, taskSummary string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		childTodo := newTodoStateStore()
		childSkills := skills.NewState()
		childSkills.SetActive(parentSkills.ActiveNames())
		childSpecs := childToolSpecs(childTodo)
		childTools := buildTools(childSpecs)
		childHandlers := buildToolHandlers(childSpecs)

		childMessages := []responses.ResponseInputItemUnionParam{
			responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
			responses.ResponseInputItemParamOfMessage("Sub-agent task summary:\n"+strings.TrimSpace(taskSummary), responses.EasyInputMessageRoleUser),
		}

		answer, err := runToolLoop(client, cfg.SubAgentModel, childTools, childHandlers, childTodo, childMessages, nil, childSkills, skillRegistry)
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return answer, nil
	}

	specs := parentToolSpecs(todo, subAgentMgr, subAgentRunner, parentSkills, skillRegistry)
	tools := buildTools(specs)
	handlers := buildToolHandlers(specs)

	messages := []responses.ResponseInputItemUnionParam{
		responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
	}

	fmt.Printf("Tool-use agent started. base_url=%s model=%s subagent_model=%s skills=%d debug_http=%t\n", cfg.BaseURL, cfg.Model, cfg.SubAgentModel, skillRegistry.Count(), cfg.DebugHTTP)
	fmt.Println("Type your message. Commands: /reset, /exit")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			fmt.Println("bye")
			return nil
		}
		if text == "/reset" {
			messages = []responses.ResponseInputItemUnionParam{
				responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
			}
			canceled := subAgentMgr.CancelAll()
			todo.Reset()
			parentSkills.Reset()
			if canceled > 0 {
				fmt.Printf("context reset (canceled %d sub-agent job(s))\n", canceled)
			} else {
				fmt.Println("context reset")
			}
			continue
		}

		// 每个用户输入视为一个新任务，清理上一轮 TODO，避免跨轮状态干扰。
		todo.Reset()
		messages = append(messages, responses.ResponseInputItemParamOfMessage(text, responses.EasyInputMessageRoleUser))
		answer, err := runToolLoop(client, cfg.Model, tools, handlers, todo, messages, subAgentMgr, parentSkills, skillRegistry)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		// Persist assistant final text into history.
		messages = append(messages, responses.ResponseInputItemParamOfMessage(answer, responses.EasyInputMessageRoleAssistant))
		fmt.Print(">>> ")
		fmt.Println(answer)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input error: %w", err)
	}
	return nil
}

func runToolLoop(
	client openai.Client,
	model string,
	tools []responses.ToolUnionParam,
	handlers map[string]toolHandler,
	todo *todoStateStore,
	messages []responses.ResponseInputItemUnionParam,
	subAgentMgr *subagent.Manager,
	skillState *skills.State,
	skillRegistry *skills.Registry,
) (string, error) {
	// inputItems 保存“真实会话历史”（用户输入、assistant 输出、tool 调用与结果）。
	inputItems := append([]responses.ResponseInputItemUnionParam{}, messages...)

	for step := 0; step < 20; step++ {
		// 每轮请求前都先做一次轻量压缩，避免 tool 结果无限膨胀。
		if compacted, _ := compact.MicroCompact(inputItems, compact.DefaultKeepRecentToolResults); compacted != nil {
			inputItems = compacted
		}
		// 达到阈值时做自动压缩（保留指令 + 最近上下文 + 摘要）。
		if compact.NeedsAutoCompact(inputItems, compact.DefaultAutoCompactCharLimit) {
			summary, err := summarizeForAutoCompact(client, model, inputItems)
			if err == nil && strings.TrimSpace(summary) != "" {
				inputItems = compact.AutoCompact(inputItems, summary, compact.DefaultAutoCompactKeepRecentK)
			}
		}

		// 每轮额外注入一次最新 TODO 摘要，让模型始终看到当前任务状态。
		// 这里不把 TODO 永久写入历史，避免上下文持续膨胀。
		requestInput := append([]responses.ResponseInputItemUnionParam{}, inputItems...)
		requestInput = append(requestInput, responses.ResponseInputItemParamOfMessage(todo.ContextMessage(), responses.EasyInputMessageRoleDeveloper))
		if skillState != nil && skillRegistry != nil {
			requestInput = append(requestInput, responses.ResponseInputItemParamOfMessage(skillRegistry.NamesContextMessage(), responses.EasyInputMessageRoleDeveloper))
			if skillCtx := strings.TrimSpace(skillState.ContextMessage(skillRegistry)); skillCtx != "" {
				requestInput = append(requestInput, responses.ResponseInputItemParamOfMessage(skillCtx, responses.EasyInputMessageRoleDeveloper))
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		params := responses.ResponseNewParams{
			Model: openai.ResponsesModel(model),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: requestInput,
			},
			Tools: tools,
		}
		resp, err := client.Responses.New(ctx, params)
		cancel()
		if err != nil {
			return "", err
		}

		followUpItems := make([]responses.ResponseInputItemUnionParam, 0, len(resp.Output)*2)
		for _, item := range resp.Output {
			if item.Type != "function_call" {
				continue
			}
			// 显式回放 function_call，便于不支持 previous_response_id tool state 的服务端匹配 call_id
			followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCall(item.Arguments, item.CallID, item.Name))

			handler, ok := handlers[item.Name]
			if !ok {
				followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, "unsupported tool"))
				continue
			}

			out := handler(item.Arguments)
			fmt.Printf("Tool use output: %s\n", out)
			followUpItems = append(followUpItems, responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, out))
		}

		if len(followUpItems) == 0 {
			if subAgentMgr != nil {
				pending := subAgentMgr.PendingCount()
				if pending > 0 {
					guard := fmt.Sprintf("There are still %d pending sub-agent job(s). Call `subagent_wait` before replying to the user.", pending)
					inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(guard, responses.EasyInputMessageRoleDeveloper))
					continue
				}
			}
			return strings.TrimSpace(resp.OutputText()), nil
		}

		inputItems = append(inputItems, followUpItems...)
	}

	return "", fmt.Errorf("tool loop exceeded max steps")
}

func summarizeForAutoCompact(
	client openai.Client,
	model string,
	items []responses.ResponseInputItemUnionParam,
) (string, error) {
	summaryInput := append([]responses.ResponseInputItemUnionParam{}, items...)
	summaryInput = append(summaryInput, responses.ResponseInputItemParamOfMessage(
		"Summarize the conversation for continuation. Keep key decisions, current progress, unresolved issues, TODO state, active skills, and any pending sub-agent work. Use concise plain text.",
		responses.EasyInputMessageRoleDeveloper,
	))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: summaryInput,
		},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.OutputText()), nil
}
