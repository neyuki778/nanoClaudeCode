package tools

import (
	"encoding/json"
	"fmt"
	"nanocc/agents/skills"
	"strings"
)

func skillListHandler(registry *skills.Registry, state *skills.State) Handler {
	return func(arguments string) string {
		if strings.TrimSpace(arguments) != "" && strings.TrimSpace(arguments) != "{}" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
				return "invalid args: " + err.Error()
			}
		}
		if registry == nil || registry.Count() == 0 {
			return "skills: none found under .skills"
		}

		activeSet := make(map[string]struct{})
		if state != nil {
			for _, name := range state.ActiveNames() {
				activeSet[name] = struct{}{}
			}
		}

		defs := registry.List()
		var b strings.Builder
		fmt.Fprintf(&b, "skills found: %d\n", len(defs))
		for _, def := range defs {
			flag := "inactive"
			if _, ok := activeSet[def.Name]; ok {
				flag = "active"
			}
			fmt.Fprintf(&b, "- %s [%s]: %s\n", def.Name, flag, def.Description)
		}
		return strings.TrimSpace(b.String())
	}
}

func skillLoadHandler(registry *skills.Registry, state *skills.State) Handler {
	return func(arguments string) string {
		if registry == nil || state == nil {
			return "error: skills runtime is not configured"
		}
		args, err := parseSkillNameArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		def, loaded, err := state.Load(args.Name, registry)
		if err != nil {
			return "error: " + err.Error()
		}
		if !loaded {
			return fmt.Sprintf("skill already active: %s", def.Name)
		}
		return fmt.Sprintf("skill loaded: %s", def.Name)
	}
}

func skillUnloadHandler(state *skills.State) Handler {
	return func(arguments string) string {
		if state == nil {
			return "error: skills runtime is not configured"
		}
		args, err := parseSkillNameArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		name := skills.NormalizeName(args.Name)
		if !state.Unload(name) {
			return fmt.Sprintf("skill not active: %s", name)
		}
		return fmt.Sprintf("skill unloaded: %s", name)
	}
}
