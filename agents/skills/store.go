package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Definition struct {
	Name        string
	Description string
	Instruction string
	Source      string
}

type Registry struct {
	skills map[string]Definition
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]Definition)}
}

func LoadRegistryFromDir(dir string) (*Registry, error) {
	registry := NewRegistry()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return registry, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			path := filepath.Join(dir, entry.Name(), "SKILL.md")
			content, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("read skill file %s: %w", path, err)
			}
			addDefinition(registry, entry.Name(), path, string(content))
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".md" && ext != ".txt" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read skill file %s: %w", path, err)
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		addDefinition(registry, name, path, string(content))
	}

	return registry, nil
}

func addDefinition(registry *Registry, rawName, source, content string) {
	meta, body := parseFrontMatter(content)

	name := NormalizeName(rawName)
	if metaName := NormalizeName(meta["name"]); metaName != "" {
		name = metaName
	}

	instruction := strings.TrimSpace(body)
	if instruction == "" {
		instruction = strings.TrimSpace(content)
	}
	if name == "" || instruction == "" {
		return
	}

	description := strings.TrimSpace(meta["description"])
	if description == "" {
		description = summarizeDescription(instruction)
	}

	def := Definition{
		Name:        name,
		Description: description,
		Instruction: instruction,
		Source:      source,
	}
	registry.skills[name] = def
}

func summarizeDescription(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 80 {
			return line[:80]
		}
		return line
	}
	return "No description"
}

func parseFrontMatter(content string) (map[string]string, string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]string{}, content
	}

	end := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			end = index
			break
		}
	}
	if end == -1 {
		return map[string]string{}, content
	}

	meta := make(map[string]string)
	for _, line := range lines[1:end] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		meta[key] = value
	}

	body := strings.Join(lines[end+1:], "\n")
	return meta, body
}

func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	return len(r.skills)
}

func (r *Registry) Get(name string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	def, ok := r.skills[NormalizeName(name)]
	return def, ok
}

func (r *Registry) List() []Definition {
	if r == nil {
		return nil
	}

	out := make([]Definition, 0, len(r.skills))
	for _, def := range r.skills {
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Names() []string {
	defs := r.List()
	out := make([]string, 0, len(defs))
	for _, def := range defs {
		out = append(out, def.Name)
	}
	return out
}

func (r *Registry) NamesContextMessage() string {
	if r == nil || r.Count() == 0 {
		return "Available skills: (none)."
	}
	return fmt.Sprintf("Available skills: %s. Use `skill_load` to activate one when needed.", strings.Join(r.Names(), ", "))
}

type State struct {
	mu     sync.RWMutex
	active map[string]struct{}
}

func NewState() *State {
	return &State{active: make(map[string]struct{})}
}

func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = make(map[string]struct{})
}

func (s *State) ActiveNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.active))
	for name := range s.active {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *State) Load(name string, registry *Registry) (Definition, bool, error) {
	name = NormalizeName(name)
	if name == "" {
		return Definition{}, false, fmt.Errorf("empty skill name")
	}
	def, ok := registry.Get(name)
	if !ok {
		return Definition{}, false, fmt.Errorf("skill not found: %s", name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[name]; exists {
		return def, false, nil
	}
	s.active[name] = struct{}{}
	return def, true, nil
}

func (s *State) Unload(name string) bool {
	name = NormalizeName(name)
	if name == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.active[name]; !exists {
		return false
	}
	delete(s.active, name)
	return true
}

func (s *State) SetActive(names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.active = make(map[string]struct{}, len(names))
	for _, name := range names {
		name = NormalizeName(name)
		if name == "" {
			continue
		}
		s.active[name] = struct{}{}
	}
}

func (s *State) ContextMessage(registry *Registry) string {
	if s == nil || registry == nil {
		return ""
	}

	names := s.ActiveNames()
	if len(names) == 0 {
		return "Active skills: (none). Use `skill_list` to discover skills and `skill_load` to activate one when needed."
	}

	const maxChars = 12000
	total := 0

	var b strings.Builder
	b.WriteString("Active skill instructions for this turn:\n")
	for _, name := range names {
		def, ok := registry.Get(name)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "[skill:%s]\n", def.Name)

		instruction := strings.TrimSpace(def.Instruction)
		if instruction == "" {
			continue
		}

		remaining := maxChars - total
		if remaining <= 0 {
			b.WriteString("(skill instructions truncated)\n")
			break
		}
		if len(instruction) > remaining {
			b.WriteString(instruction[:remaining])
			b.WriteString("\n(skill instructions truncated)\n")
			break
		}

		b.WriteString(instruction)
		b.WriteString("\n")
		total += len(instruction)
	}
	return strings.TrimSpace(b.String())
}

func NormalizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
