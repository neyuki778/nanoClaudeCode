package runtime

import (
	"bufio"
	"context"
	"fmt"
	"nanocc/agents/background"
	"nanocc/agents/compact"
	rtools "nanocc/agents/runtime/tools"
	"nanocc/agents/sessions"
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
	sessionStore := sessions.NewStore(".sessions")
	activeSessionID := ""

	todo := rtools.NewTodoStore()
	backgroundMgr := background.NewManager()
	subAgentMgr := subagent.NewManager(4)
	subAgentRunner := func(ctx context.Context, taskSummary string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		childTodo := rtools.NewTodoStore()
		childSkills := skills.NewState()
		childSkills.SetActive(parentSkills.ActiveNames())
		childSpecs := rtools.ChildSpecs(childTodo)
		childTools := rtools.BuildTools(childSpecs)
		childHandlers := rtools.BuildHandlers(childSpecs)

		childMessages := []responses.ResponseInputItemUnionParam{
			responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
			responses.ResponseInputItemParamOfMessage("Sub-agent task summary:\n"+strings.TrimSpace(taskSummary), responses.EasyInputMessageRoleUser),
		}

		answer, err := runToolLoop(client, cfg.SubAgentModel, childTools, childHandlers, childTodo, childMessages, nil, nil, childSkills, skillRegistry)
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return answer, nil
	}

	specs := rtools.ParentSpecs(todo, backgroundMgr, subAgentMgr, subAgentRunner, parentSkills, skillRegistry)
	tools := rtools.BuildTools(specs)
	handlers := rtools.BuildHandlers(specs)

	messages := []responses.ResponseInputItemUnionParam{
		responses.ResponseInputItemParamOfMessage(developerMessage, responses.EasyInputMessageRoleDeveloper),
	}

	fmt.Printf("Tool-use agent started. base_url=%s model=%s subagent_model=%s skills=%d debug_http=%t\n", cfg.BaseURL, cfg.Model, cfg.SubAgentModel, skillRegistry.Count(), cfg.DebugHTTP)
	if currentID, err := sessionStore.CurrentID(); err != nil {
		fmt.Printf("warning: failed to inspect saved session: %v\n", err)
	} else if currentID != "" {
		if snapshot, err := sessionStore.LoadCurrent(); err == nil && snapshot != nil {
			fmt.Printf("Saved current session %s from %s. Use /resume or /resume %s to restore it.\n", currentID, snapshot.SavedAt.Format(time.RFC3339), currentID)
		}
	}
	fmt.Println("Type your message. Commands: /sessions, /resume [id], /reset, /exit")

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
		if text == "/sessions" {
			ids, err := sessionStore.ListSessions()
			if err != nil {
				fmt.Printf("error: failed to list sessions: %v\n", err)
				continue
			}
			currentID, _ := sessionStore.CurrentID()
			if len(ids) == 0 {
				fmt.Println("no saved sessions found")
				continue
			}
			fmt.Println("saved sessions:")
			for _, id := range ids {
				flag := ""
				if id == currentID {
					flag = " [current]"
				}
				fmt.Printf("- %s%s\n", id, flag)
			}
			continue
		}
		if strings.HasPrefix(text, "/resume") {
			sessionID := strings.TrimSpace(strings.TrimPrefix(text, "/resume"))
			if sessionID == "" {
				ids, err := sessionStore.ListSessions()
				if err != nil {
					fmt.Printf("error: failed to list sessions: %v\n", err)
					continue
				}
				currentID, _ := sessionStore.CurrentID()
				if len(ids) == 0 {
					fmt.Println("no saved sessions found")
					continue
				}
				fmt.Println("saved sessions:")
				for _, id := range ids {
					snapshot, err := sessionStore.Load(id)
					if err != nil {
						fmt.Printf("- %s (failed to load preview: %v)\n", id, err)
						continue
					}
					flag := ""
					if id == currentID {
						flag = " [current]"
					}
					preview := sessions.FirstUserMessagePreview(snapshot.Messages, 48)
					fmt.Printf("- %s%s | first user message: %q\n", id, flag, preview)
				}
				fmt.Println("Use /resume <session_id> to switch to a saved session.")
				continue
			}
			snapshot, resumedID, err := sessionStore.Resume(sessionID)
			if err != nil {
				fmt.Printf("error: failed to load saved session: %v\n", err)
				continue
			}
			if snapshot == nil {
				if sessionID == "" {
					fmt.Println("no saved current session found")
				} else {
					fmt.Printf("session not found: %s\n", sessionID)
				}
				continue
			}

			loadedMessages, err := sessions.DecodeMessages(snapshot.Messages)
			if err != nil {
				fmt.Printf("error: invalid saved session: %v\n", err)
				continue
			}
			messages = loadedMessages
			if err := todo.Import(snapshot.Todo); err != nil {
				fmt.Printf("warning: failed to restore todo state: %v\n", err)
				todo.Reset()
			}
			parentSkills.SetActive(snapshot.ActiveSkills)
			activeSessionID = resumedID
			fmt.Printf("session resumed: %s (%d message(s), %d active skill(s), saved_at=%s)\n", resumedID, len(messages), len(snapshot.ActiveSkills), snapshot.SavedAt.Format(time.RFC3339))
			continue
		}
		if text == "/reset" {
			if archived, err := sessionStore.ArchiveCurrent(); err != nil {
				fmt.Printf("warning: failed to archive session: %v\n", err)
			} else if archived != "" {
				fmt.Printf("archived session to %s\n", archived)
			}
			if err := sessionStore.ClearCurrent(); err != nil {
				fmt.Printf("warning: failed to clear current session: %v\n", err)
			}
			activeSessionID = ""
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
		answer, err := runToolLoop(client, cfg.Model, tools, handlers, todo, messages, backgroundMgr, subAgentMgr, parentSkills, skillRegistry)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			continue
		}

		// Persist assistant final text into history.
		messages = append(messages, responses.ResponseInputItemParamOfMessage(answer, responses.EasyInputMessageRoleAssistant))
		sessionID, err := sessionStore.Save(activeSessionID, messages, todo, parentSkills)
		if err != nil {
			fmt.Printf("warning: failed to save session: %v\n", err)
		} else if sessionID != "" {
			activeSessionID = sessionID
			fmt.Printf("[session %s saved]\n", sessionID)
		}
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
	handlers map[string]rtools.Handler,
	todo *rtools.TodoStore,
	messages []responses.ResponseInputItemUnionParam,
	backgroundMgr *background.Manager,
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
		if backgroundMgr != nil {
			if notes := strings.TrimSpace(rtools.FormatBackgroundNotifications(backgroundMgr.DrainNotifications())); notes != "" {
				requestInput = append(requestInput, responses.ResponseInputItemParamOfMessage(notes, responses.EasyInputMessageRoleDeveloper))
			}
		}
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
