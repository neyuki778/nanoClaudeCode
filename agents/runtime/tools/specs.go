package tools

import (
	"nanocc/agents/skills"
	"nanocc/agents/subagent"
)

func baseSpecs(todo *TodoStore) []Spec {
	return []Spec{
		newTool(
			"bash",
			"Execute a shell command in current workspace and return stdout/stderr.",
			bashHandler,
			reqString("command", "Shell command to run"),
		),
		newTool(
			"read_file",
			"Read file contents from workspace.",
			readFileHandler,
			reqString("path", "Relative file path to read"),
			optInteger("limit", "Max bytes to return (optional)"),
		),
		newTool(
			"write_file",
			"Write file contents into workspace.",
			writeFileHandler,
			reqString("path", "Relative file path to write"),
			reqString("content", "File content to write"),
		),
		{
			Name:        "todo_set",
			Description: "Replace TODO state with latest full list and current task.",
			Parameters:  todoSetSchema(),
			Handler:     todoSetHandler(todo),
		},
	}
}

func ParentSpecs(
	todo *TodoStore,
	manager *subagent.Manager,
	runner subagent.Runner,
	skillState *skills.State,
	skillRegistry *skills.Registry,
) []Spec {
	specs := append([]Spec{}, baseSpecs(todo)...)
	specs = append(specs,
		Spec{
			Name:        "skill_list",
			Description: "List all skills discovered from .skills directory and show active flags.",
			Parameters:  skillListSchema(),
			Handler:     skillListHandler(skillRegistry, skillState),
		},
		Spec{
			Name:        "skill_load",
			Description: "Activate one skill by name for subsequent turns.",
			Parameters:  skillNameSchema("Skill name to activate."),
			Handler:     skillLoadHandler(skillRegistry, skillState),
		},
		Spec{
			Name:        "skill_unload",
			Description: "Deactivate one skill by name.",
			Parameters:  skillNameSchema("Skill name to deactivate."),
			Handler:     skillUnloadHandler(skillState),
		},
		Spec{
			Name:        "subagent_spawn",
			Description: "Create and start one sub-agent job asynchronously.",
			Parameters:  subAgentSpawnSchema(),
			Handler:     subAgentSpawnHandler(manager, runner),
		},
		Spec{
			Name:        "subagent_wait",
			Description: "Wait for specific sub-agent jobs (or all when omitted) and return summaries.",
			Parameters:  subAgentWaitSchema(),
			Handler:     subAgentWaitHandler(manager),
		},
	)
	return specs
}

func ChildSpecs(todo *TodoStore) []Spec {
	return append([]Spec{}, baseSpecs(todo)...)
}
