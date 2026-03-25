package tools

func todoSetSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "Full task list.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Stable task id, e.g. t1.",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "Task description.",
						},
						"done": map[string]any{
							"type":        "boolean",
							"description": "Whether task is completed.",
						},
					},
					"required": []string{"id", "text", "done"},
				},
			},
			"current_id": map[string]any{
				"type":        "string",
				"description": "Current in-progress task id; empty means none.",
			},
		},
		"required": []string{"tasks", "current_id"},
	}
}

func subAgentSpawnSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task_summary": map[string]any{
				"type":        "string",
				"description": "Task summary for the sub-agent to execute.",
			},
			"timeout_sec": map[string]any{
				"type":        "integer",
				"description": "Per-attempt timeout in seconds.",
			},
			"retries": map[string]any{
				"type":        "integer",
				"description": "Retry count after failure. Max 2.",
			},
		},
		"required": []string{"task_summary", "timeout_sec"},
	}
}

func subAgentWaitSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"agent_ids": map[string]any{
				"type":        "array",
				"description": "Target sub-agent IDs. Empty means wait all.",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}
}

func skillListSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

func skillNameSchema(description string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": description,
			},
		},
		"required": []string{"name"},
	}
}
