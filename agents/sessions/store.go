package sessions

import (
	"encoding/json"
	"fmt"
	rtools "nanocc/agents/runtime/tools"
	"nanocc/agents/skills"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/responses"
)

const Version = 1

type Item struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	Name      string `json:"name,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type Snapshot struct {
	Version      int                  `json:"version"`
	SavedAt      time.Time            `json:"saved_at"`
	Messages     []Item               `json:"messages"`
	Summary      string               `json:"summary,omitempty"`
	Todo         rtools.PersistedTodo `json:"todo"`
	ActiveSkills []string             `json:"active_skills,omitempty"`
}

type Store struct {
	root       string
	current    string
	archiveDir string
}

func NewStore(root string) *Store {
	if strings.TrimSpace(root) == "" {
		root = ".sessions"
	}
	return &Store{
		root:       root,
		current:    filepath.Join(root, "current"),
		archiveDir: filepath.Join(root, "archive"),
	}
}

func (s *Store) LoadCurrent() (*Snapshot, error) {
	id, err := s.CurrentID()
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	return s.Load(id)
}

func (s *Store) Load(id string) (*Snapshot, error) {
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *Store) CurrentID() (string, error) {
	data, err := os.ReadFile(s.current)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *Store) SaveCurrent(messages []responses.ResponseInputItemUnionParam, todo *rtools.TodoStore, skillState *skills.State) (string, error) {
	sessionID, err := s.CurrentID()
	if err != nil {
		return "", err
	}
	return s.Save(sessionID, messages, todo, skillState)
}

func (s *Store) Save(sessionID string, messages []responses.ResponseInputItemUnionParam, todo *rtools.TodoStore, skillState *skills.State) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = newSessionID()
	}
	snap := Snapshot{
		Version:      Version,
		SavedAt:      time.Now().UTC(),
		Messages:     EncodeMessages(messages),
		Summary:      ExtractSummary(messages),
		ActiveSkills: nil,
	}
	if todo != nil {
		snap.Todo = todo.Export()
	}
	if skillState != nil {
		snap.ActiveSkills = skillState.ActiveNames()
	}
	if err := s.writeJSONAtomic(s.sessionPath(sessionID), snap); err != nil {
		return "", err
	}
	if err := s.writeCurrentPointer(sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Store) Resume(id string) (*Snapshot, string, error) {
	if strings.TrimSpace(id) == "" {
		id, _ = s.CurrentID()
	}
	if strings.TrimSpace(id) == "" {
		return nil, "", nil
	}
	snap, err := s.Load(id)
	if err != nil || snap == nil {
		return snap, id, err
	}
	if err := s.writeCurrentPointer(id); err != nil {
		return nil, "", err
	}
	return snap, id, nil
}

func (s *Store) ListSessions() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "current" || !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func (s *Store) ArchiveCurrent() (string, error) {
	id, err := s.CurrentID()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", nil
	}
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if err := os.MkdirAll(s.archiveDir, 0o755); err != nil {
		return "", err
	}

	name := time.Now().UTC().Format("20060102T150405Z") + ".json"
	dst := filepath.Join(s.archiveDir, name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *Store) ClearCurrent() error {
	err := os.Remove(s.current)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) writeJSONAtomic(path string, payload any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) writeCurrentPointer(id string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	tmp := s.current + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(id)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.current)
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.root, strings.TrimSpace(id)+".json")
}

func newSessionID() string {
	now := time.Now().UTC()
	return now.Format("20060102T150405") + fmt.Sprintf(".%09dZ", now.Nanosecond())
}

func EncodeMessages(items []responses.ResponseInputItemUnionParam) []Item {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		switch {
		case item.OfMessage != nil:
			out = append(out, Item{
				Type:    "message",
				Role:    string(item.OfMessage.Role),
				Content: item.OfMessage.Content.OfString.Or(""),
			})
		case item.OfFunctionCall != nil:
			out = append(out, Item{
				Type:      "function_call",
				Name:      item.OfFunctionCall.Name,
				CallID:    item.OfFunctionCall.CallID,
				Arguments: item.OfFunctionCall.Arguments,
			})
		case item.OfFunctionCallOutput != nil:
			out = append(out, Item{
				Type:   "function_call_output",
				CallID: item.OfFunctionCallOutput.CallID,
				Output: item.OfFunctionCallOutput.Output.OfString.Or(""),
			})
		}
	}
	return out
}

func DecodeMessages(items []Item) ([]responses.ResponseInputItemUnionParam, error) {
	out := make([]responses.ResponseInputItemUnionParam, 0, len(items))
	for index, item := range items {
		switch strings.TrimSpace(item.Type) {
		case "message":
			role := normalizeMessageRole(item.Role)
			out = append(out, responses.ResponseInputItemParamOfMessage(item.Content, role))
		case "function_call":
			if strings.TrimSpace(item.CallID) == "" || strings.TrimSpace(item.Name) == "" {
				return nil, fmt.Errorf("messages[%d]: invalid function_call", index)
			}
			out = append(out, responses.ResponseInputItemParamOfFunctionCall(item.Arguments, item.CallID, item.Name))
		case "function_call_output":
			if strings.TrimSpace(item.CallID) == "" {
				return nil, fmt.Errorf("messages[%d]: invalid function_call_output", index)
			}
			out = append(out, responses.ResponseInputItemParamOfFunctionCallOutput(item.CallID, item.Output))
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported type %q", index, item.Type)
		}
	}
	return out, nil
}

func ExtractSummary(items []responses.ResponseInputItemUnionParam) string {
	const prefix = "Conversation summary (auto-compact):\n"
	for index := len(items) - 1; index >= 0; index-- {
		item := items[index]
		if item.OfMessage == nil || item.OfMessage.Role != responses.EasyInputMessageRoleDeveloper {
			continue
		}
		text := item.OfMessage.Content.OfString.Or("")
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	return ""
}

func FirstUserMessagePreview(items []Item, maxChars int) string {
	for _, item := range items {
		if strings.TrimSpace(item.Type) != "message" {
			continue
		}
		if normalizeMessageRole(item.Role) != responses.EasyInputMessageRoleUser {
			continue
		}
		return shortenPreview(item.Content, maxChars)
	}
	return "(no user message)"
}

func shortenPreview(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "(empty)"
	}
	if maxChars <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars == 1 {
		return string(runes[:1])
	}
	return string(runes[:maxChars-1]) + "…"
}

func normalizeMessageRole(role string) responses.EasyInputMessageRole {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return responses.EasyInputMessageRoleSystem
	case "assistant":
		return responses.EasyInputMessageRoleAssistant
	case "user":
		return responses.EasyInputMessageRoleUser
	default:
		return responses.EasyInputMessageRoleDeveloper
	}
}
