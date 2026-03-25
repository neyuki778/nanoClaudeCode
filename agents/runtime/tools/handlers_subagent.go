package tools

import (
	"fmt"
	"nanocc/agents/subagent"
	"strings"
	"time"
)

func subAgentSpawnHandler(manager *subagent.Manager, runner subagent.Runner) Handler {
	return func(arguments string) string {
		if manager == nil {
			return "error: subagent manager is not configured"
		}
		args, err := parseSubAgentSpawnArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}
		if args.Retries == 0 {
			args.Retries = 2
		}

		jobID, err := manager.Spawn(
			args.TaskSummary,
			time.Duration(args.TimeoutSec)*time.Second,
			args.Retries,
			runner,
		)
		if err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("subagent spawned: %s", jobID)
	}
}

func subAgentWaitHandler(manager *subagent.Manager) Handler {
	return func(arguments string) string {
		if manager == nil {
			return "error: subagent manager is not configured"
		}
		args, err := parseSubAgentWaitArgs(arguments)
		if err != nil {
			return "invalid args: " + err.Error()
		}

		jobs := manager.Wait(args.AgentIDs)
		return formatWaitResult(jobs)
	}
}

func formatWaitResult(jobs []subagent.Job) string {
	if len(jobs) == 0 {
		return "no subagent jobs found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "subagent wait finished: %d job(s)\n", len(jobs))
	for _, job := range jobs {
		fmt.Fprintf(&b, "- %s [%s] attempt=%d", job.ID, job.Status, job.Attempt)
		if job.Status == subagent.JobSucceeded && strings.TrimSpace(job.ResultText) != "" {
			fmt.Fprintf(&b, "\n  result: %s", strings.TrimSpace(job.ResultText))
		}
		if job.Status != subagent.JobSucceeded && strings.TrimSpace(job.ErrorText) != "" {
			fmt.Fprintf(&b, "\n  error: %s", strings.TrimSpace(job.ErrorText))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
