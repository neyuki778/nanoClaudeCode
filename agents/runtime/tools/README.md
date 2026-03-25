# runtime/tools

`agents/runtime/tools` 是主运行时的工具层，按职责拆分，避免单文件膨胀。

- `types.go`：工具规格与构建器（`Spec`、`BuildTools`、`BuildHandlers`）
- `specs.go`：父/子代理工具清单组装（`ParentSpecs`、`ChildSpecs`）
- `todo_store.go`：`todo_set` 相关状态存储与校验
- `schemas.go`：各工具的 JSON Schema
- `parse.go`：各工具参数解析
- `handlers_base.go`：`bash` / `read_file` / `write_file`
- `handlers_background.go`：`bash_bg` / `bg_wait` / `bg_list`
- `handlers_todo.go`：`todo_set`
- `handlers_skill.go`：`skill_list` / `skill_load` / `skill_unload`
- `handlers_subagent.go`：`subagent_spawn` / `subagent_wait`

约定：新增工具时，优先复用现有 parser/schema 模块，并按领域放入对应 handler 文件。
