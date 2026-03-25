package subagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCanceled  JobStatus = "canceled"
)

type Job struct {
	ID          string
	TaskSummary string
	Timeout     time.Duration
	MaxRetries  int
	Attempt     int

	Status     JobStatus
	ResultText string
	ErrorText  string

	// Done 在任务进入终态时关闭；Wait 通过它阻塞等待。
	Done   chan struct{}
	cancel context.CancelFunc
}

type Runner func(ctx context.Context, taskSummary string) (string, error)

type Manager struct {
	mu sync.RWMutex

	// sem 是并发限流器：容量即最大并发子代理数。
	sem    chan struct{}
	nextID int64
	jobs   map[string]*Job
}

func NewManager(maxParallel int) *Manager {
	if maxParallel <= 0 {
		maxParallel = 4
	}
	return &Manager{
		sem:  make(chan struct{}, maxParallel),
		jobs: make(map[string]*Job),
	}
}

func (m *Manager) Spawn(taskSummary string, timeout time.Duration, maxRetries int, runner Runner) (string, error) {
	taskSummary = strings.TrimSpace(taskSummary)
	if taskSummary == "" {
		return "", fmt.Errorf("empty task summary")
	}
	if runner == nil {
		return "", fmt.Errorf("runner is nil")
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries > 2 {
		maxRetries = 2
	}

	baseCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("sa-%d", m.nextID)
	job := &Job{
		ID:          id,
		TaskSummary: taskSummary,
		Timeout:     timeout,
		MaxRetries:  maxRetries,
		Status:      JobQueued,
		Done:        make(chan struct{}),
		cancel:      cancel,
	}
	m.jobs[id] = job
	m.mu.Unlock()

	// 异步执行：Spawn 必须快速返回，主代理才能继续创建更多子代理。
	go m.runJob(baseCtx, job, runner)
	return id, nil
}

func (m *Manager) runJob(baseCtx context.Context, job *Job, runner Runner) {
	defer close(job.Done)
	defer job.cancel()

	select {
	case m.sem <- struct{}{}:
		// 获取并发槽位成功。
		defer func() { <-m.sem }()
	case <-baseCtx.Done():
		// 任务在启动前就被取消。
		m.setFinal(job.ID, JobCanceled, "", errString(baseCtx.Err()))
		return
	}

	// 总尝试次数 = 首次执行 + 重试次数。
	totalAttempts := job.MaxRetries + 1
	for attempt := 1; attempt <= totalAttempts; attempt++ {
		if baseCtx.Err() != nil {
			m.setFinal(job.ID, JobCanceled, "", errString(baseCtx.Err()))
			return
		}

		m.setRunning(job.ID, attempt)

		attemptCtx := baseCtx
		cancel := func() {}
		if job.Timeout > 0 {
			// 每次尝试都有独立超时，避免单次执行无限阻塞。
			attemptCtx, cancel = context.WithTimeout(baseCtx, job.Timeout)
		}
		result, err := runner(attemptCtx, job.TaskSummary)
		cancel()

		if err == nil {
			m.setFinal(job.ID, JobSucceeded, strings.TrimSpace(result), "")
			return
		}
		if baseCtx.Err() != nil {
			m.setFinal(job.ID, JobCanceled, "", errString(baseCtx.Err()))
			return
		}
		if attempt == totalAttempts {
			// 重试已耗尽，进入失败终态。
			m.setFinal(job.ID, JobFailed, "", strings.TrimSpace(err.Error()))
			return
		}
	}
}

func (m *Manager) Wait(ids []string) []Job {
	jobs := m.pickJobs(ids)
	for _, job := range jobs {
		// 等待每个目标子代理结束（Done close）。
		<-job.Done
	}
	return m.Snapshot(ids)
}

func (m *Manager) WaitAll() []Job {
	return m.Wait(nil)
}

func (m *Manager) Snapshot(ids []string) []Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	selectedIDs := m.pickIDsLocked(ids)
	out := make([]Job, 0, len(selectedIDs))
	for _, id := range selectedIDs {
		job := m.jobs[id]
		cp := *job
		cp.Done = nil
		cp.cancel = nil
		out = append(out, cp)
	}
	return out
}

func (m *Manager) PendingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, job := range m.jobs {
		if job.Status == JobQueued || job.Status == JobRunning {
			count++
		}
	}
	return count
}

func (m *Manager) CancelAll() int {
	m.mu.RLock()
	cancels := make([]context.CancelFunc, 0, len(m.jobs))
	for _, job := range m.jobs {
		if isFinalStatus(job.Status) {
			continue
		}
		if job.cancel != nil {
			cancels = append(cancels, job.cancel)
		}
	}
	m.mu.RUnlock()

	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

func (m *Manager) setRunning(id string, attempt int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || isFinalStatus(job.Status) {
		return
	}
	job.Status = JobRunning
	job.Attempt = attempt
}

func (m *Manager) setFinal(id string, status JobStatus, resultText, errText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || isFinalStatus(job.Status) {
		// 终态不可重复写，避免并发竞争覆盖结果。
		return
	}
	job.Status = status
	job.ResultText = strings.TrimSpace(resultText)
	job.ErrorText = strings.TrimSpace(errText)
}

func (m *Manager) pickJobs(ids []string) []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	selectedIDs := m.pickIDsLocked(ids)
	out := make([]*Job, 0, len(selectedIDs))
	for _, id := range selectedIDs {
		out = append(out, m.jobs[id])
	}
	return out
}

func (m *Manager) pickIDsLocked(ids []string) []string {
	if len(ids) == 0 {
		out := make([]string, 0, len(m.jobs))
		for id := range m.jobs {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}

	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := m.jobs[id]; !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func isFinalStatus(status JobStatus) bool {
	return status == JobSucceeded || status == JobFailed || status == JobCanceled
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
