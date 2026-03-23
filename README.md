# nanoClaudeCode

用 Go 实现的 Coding Agent Harness，逐步还原 Claude Code 的核心机制。

参考自 [learn-claude-code](./ref/learn-claude-code-main) 的 12 个递进式课程，以 Python 参考实现为蓝图，用 Go 重新构建每一层 harness 机制。

---

## 核心理念

**Agent 是模型，不是框架。**

```
Harness = Tools + Knowledge + Observation + Action Interfaces + Permissions

模型做决策，Harness 执行。
模型做推理，Harness 提供上下文。
```

Agent 循环的最小形式：

```
User → messages[] → LLM → response
                             |
                   stop_reason == tool_use?
                  /                        \
                yes                         no
                 |                           |
           execute tools               return text
           append results
           loop ──────────────> messages[]
```

循环永远不变。每个课程只在循环之上叠加一个 harness 机制。

---

## 当前实现

### `demo/cmd/tool_use` — 带 TODO 状态管理的工具调用 Agent

核心特性：
- **4 个工具**：`bash`、`read_file`、`write_file`、`todo_set`
- **TODO 状态管理**：三阶段状态机（`empty` → `active` → `completed`），完整校验
- **上下文注入**：每轮请求自动将 TODO 状态注入 system 消息
- **工具调度**：dispatch map（`toolName → handler`），循环结构不动，新工具注册即用
- **安全机制**：路径遍历防护、危险命令黑名单、文件大小限制、命令超时（30s）

```
demo/
├── main.go                    # 简单 API 调用示例
├── go.mod / go.sum
├── cmd/
│   ├── chatbot/main.go        # 无工具对话 agent（12 轮上下文窗口）
│   └── tool_use/
│       ├── main.go            # 工具调用 agent 主循环（最多 20 步）
│       └── tools.go           # 工具实现 + TODO 状态机 + Schema 生成
└── internal/common/
    └── config.go              # 配置加载（BaseURL / APIKey / Model）
```

### `demo/cmd/chatbot` — 基础对话 Agent

- 多轮对话，保留最近 12 条历史
- 使用 Responses API string input 模式

---

## 路线图

对应 [ref/learn-claude-code-main/agents](./ref/learn-claude-code-main/agents) 的 12 个课程：

| 课程 | 主题 | 格言 | 状态 |
|------|------|------|------|
| s01 | Agent 循环 | *One loop & Bash is all you need* | ✅ 已实现 |
| s02 | Tool Use | *加一个工具，只加一个 handler* | ✅ 已实现 |
| s03 | TodoWrite | *没有计划的 agent 走哪算哪* | ✅ 已实现 |
| s04 | 子智能体 | *大任务拆小，每个小任务干净的上下文* | 📋 待实现 |
| s05 | Skills | *用到什么知识，临时加载什么知识* | 📋 待实现 |
| s06 | Context Compact | *上下文总会满，要有办法腾地方* | 📋 待实现 |
| s07 | 任务系统 | *大目标要拆成小任务，排好序，记在磁盘上* | 📋 待实现 |
| s08 | 后台任务 | *慢操作丢后台，agent 继续想下一步* | 📋 待实现 |
| s09 | 智能体团队 | *任务太大一个人干不完，要能分给队友* | 📋 待实现 |
| s10 | 团队协议 | *队友之间要有统一的沟通规矩* | 📋 待实现 |
| s11 | 自治智能体 | *队友自己看看板，有活就认领* | 📋 待实现 |
| s12 | Worktree 隔离 | *各干各的目录，互不干扰* | 📋 待实现 |

---

## 快速开始

### 环境配置

```sh
cp demo/.env.example demo/.env  # 编辑填入 API Key
```

`.env` 变量：

```env
OPENAI_BASE_URL=https://api.anthropic.com/v1   # 或自定义 endpoint
OPENAI_API_KEY=your-api-key
OPENAI_MODEL=claude-sonnet-4-6
DEBUG_HTTP=false
```

### 运行工具调用 Agent

```sh
cd demo
go run ./cmd/tool_use
```

支持命令：
- 直接输入任务，agent 会调用工具完成
- `/reset` — 清空对话历史

### 运行对话 Agent

```sh
cd demo
go run cmd/chatbot/main.go
```

---

## 依赖

```
github.com/openai/openai-go/v3   # OpenAI-compatible Go SDK（兼容 Responses API）
github.com/joho/godotenv         # .env 文件加载
```

---

## 目录结构

```
nanoClaudeCode/
├── README.md
├── demo/                          # Go 实现
│   ├── cmd/
│   │   ├── chatbot/               # s01: 基础 agent 循环（无工具）
│   │   └── tool_use/              # s01-s03: 工具调用 + TODO 管理
│   └── internal/common/           # 配置工具
├── docs/
│   └── response-api.md            # Responses API 参考文档
└── ref/
    └── learn-claude-code-main/    # Python 参考实现（s01-s12）
```

---

**模型就是 Agent。代码是 Harness。造好 Harness，Agent 会完成剩下的。**
