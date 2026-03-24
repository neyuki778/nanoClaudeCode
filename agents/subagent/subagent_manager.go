package main

import (
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type subAgentJobStatus string

const (
	subAgentJobQueued    subAgentJobStatus = "queued"
	subAgentJobRunning   subAgentJobStatus = "running"
	subAgentJobSucceeded subAgentJobStatus = "succeeded"
	subAgentJobFailed    subAgentJobStatus = "failed"
	subAgentJobCanceled  subAgentJobStatus = "canceled"
)

type subAgentJob struct {
	ID          string
	TaskSummary string
	Timeout     time.Duration
	MaxRetries  int
	Attempt     int

	Status     subAgentJobStatus
	ResultText string
	ErrorText  string

	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Done       chan struct{}
}

type subAgentRuntime struct {
	Client   openai.Client
	Model    string
	Tools    []responses.ToolUnionParam
	Handlers map[string]toolHandler
}

type subAgentManager struct {
	mu sync.RWMutex

	maxParallel int
	sem         chan struct{}
	nextID      int64
	jobs        map[string]*subAgentJob
}

func newSubAgentManager(maxParallel int) *subAgentManager {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	return &subAgentManager{
		maxParallel: maxParallel,
		sem:         make(chan struct{}, maxParallel),
		jobs:        make(map[string]*subAgentJob),
	}
}
