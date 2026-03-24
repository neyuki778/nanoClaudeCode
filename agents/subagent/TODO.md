# SubAgent 实现 TODO（MVP）

## 目标
- 在 `agents/subagent` 内实现“父代理可并发调度子代理”的 tool-use 架构。
- 子代理作为工具能力暴露给父代理（`subagent_spawn` / `subagent_wait`）。
- 父代理在有未完成子代理时不得结束当前任务。

## 已确认需求
- 子代理通过工具触发（不是隐式内部逻辑）。
- 子代理**不可递归创建**子代理。
- 子代理输入上下文只传 `task_summary`。
- 子代理可用工具：除 `subagent_*` 之外与父代理一致（MVP）。
- 子代理结果当作普通文本输出即可。
- 子代理使用独立 TODO 状态，不与父代理共享。
- `timeout_sec` 由父代理在 `subagent_spawn` 参数中指定。
- 失败重试：整次子代理执行失败后重跑，最多 2 次。
- 支持并发子代理，最大并发数固定 `4`。
- MVP 主/子代理先使用同一模型。
- 父代理必须同步：等待相关子代理完成后才进入下一步。
- `/reset` 必须取消全部未完成子代理。
- 父代理最终回复前必须确保无 pending 子代理。

## 架构思路
- 抽象通用执行器 `runAgentLoop(...)`，父/子代理共用同一套 loop。
- 工具分层：
  - `baseTools`: `bash/read_file/write_file/todo_set`
  - `parentTools`: `baseTools + subagent_spawn + subagent_wait`
  - `childTools`: `baseTools`（禁用 `subagent_*`）
- 引入 `subAgentManager` 管理生命周期、并发、等待、取消与结果汇总。
- `subagent_spawn` 只负责创建并启动异步任务，返回 `agent_id`。
- `subagent_wait` 负责阻塞等待（指定 `agent_ids` 或全部）并返回汇总文本。

## 状态机

```text
Parent Agent (tool-use loop)
┌──────────────┐
│    RUNNING   │
└──────┬───────┘
       │ tool: subagent_spawn(task_summary, timeout, retries)
       v
┌──────────────────────────┐
│ SPAWNING (quick return)  │
│ - create job_id          │
│ - start goroutine        │
│ - return job_id          │
└──────┬───────────────────┘
       │ (can repeat multiple times)
       v
┌──────────────────────────┐
│ CHILDREN_PENDING > 0     │
└──────┬───────────────────┘
       │ tool: subagent_wait(ids|all)
       v
┌──────────────────────────┐
│ WAITING (blocking)       │
│ - wait until done        │
│ - collect summaries      │
└──────┬───────────────────┘
       │ all waited
       v
┌──────────────┐
│   RUNNING    │
└──────┬───────┘
       │ no pending children + no function_call
       v
┌──────────────┐
│    FINAL     │
└──────────────┘

Guard Rule:
- If CHILDREN_PENDING > 0, parent cannot FINAL.
- Runtime injects guard message to force `subagent_wait`.


Child Agent Job (managed by subAgentManager)
┌─────────┐
│ CREATED │
└────┬────┘
     │ acquire semaphore(max=4)
     v
┌─────────┐
│ RUNNING │ ---> run child tool-use loop (child tools only, own TODO)
└────┬────┘
     │ success
     ├──────────────────────────► ┌───────────┐
     │                            │ SUCCEEDED │
     │ failure + retries left     └─────┬─────┘
     ├──► retry (max 2)                │ wait collected
     │                                  v
     │                            ┌───────────┐
     └──────── failure no retry ─►│ COLLECTED │
                                  └───────────┘

On /reset:
- parent calls CancelAll()
- running/queued child jobs move to CANCELED
```

## 关键实现项（按顺序）
- [ ] 1. 设计 `subAgentJob` / `subAgentManager` 数据结构与线程安全访问。
- [ ] 2. 实现并发控制（semaphore，max=4）与任务状态机（running/succeeded/failed/canceled）。
- [ ] 3. 抽取通用 `runAgentLoop(...)`，避免父/子代理逻辑重复。
- [ ] 4. 在工具注册层实现 `baseTools/parentTools/childTools` 组装。
- [ ] 5. 实现 `subagent_spawn` schema 与 handler（含 `task_summary`、`timeout_sec`、`retries`）。
- [ ] 6. 实现 `subagent_wait` schema 与 handler（阻塞等待并汇总纯文本结果）。
- [ ] 7. 父代理结束前增加 guard：若有 pending 子代理则拒绝结束并提示继续调用等待工具。
- [ ] 8. `/reset` 逻辑接入 `CancelAll()`，确保子代理被清理。
- [ ] 9. 子代理独立 TODO store 接入与上下文注入。
- [ ] 10. 补充最小验证：编译通过 + 手动场景（单子代理/并发4个/超时重试/Reset取消）。

## 参数草案（MVP）
- `subagent_spawn`:
  - `task_summary` (string, required)
  - `timeout_sec` (integer, required, 建议范围 30~600)
  - `retries` (integer, optional, 默认 2，MVP 可固定上限 2)
- `subagent_wait`:
  - `agent_ids` (array[string], optional；空则等待全部)

## 验收标准
- 父代理可在同一轮 spawn 多个子代理并并发执行。
- 父代理在 pending 子代理存在时无法直接完成回复。
- 子代理不会调用 `subagent_*` 工具（无递归）。
- 子代理失败会按规则重试并给出最终状态。
- `/reset` 后无残留运行中的子代理任务。
