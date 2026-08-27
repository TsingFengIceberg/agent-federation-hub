package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource already exists")
)

type Store interface {
	PutAgent(context.Context, Agent) error
	GetAgent(context.Context, string, string) (Agent, error)
	ListAgents(context.Context, string) ([]Agent, error)
	CreateTask(context.Context, Task, Event) (Task, error)
	ApplyTask(context.Context, string, string, string, func(*Task) (Event, error)) (Task, bool, error)
	GetTask(context.Context, string, string) (Task, error)
	ListRecoverable(context.Context) ([]Task, error)
	EventsAfter(context.Context, string, string, uint64) ([]Event, error)
	Close() error
}

type journalRecord struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Agent   *Agent `json:"agent,omitempty"`
	Task    *Task  `json:"task,omitempty"`
	Event   *Event `json:"event,omitempty"`
}

type JournalStore struct {
	mu        sync.RWMutex
	file      *os.File
	agents    map[string]Agent
	tasks     map[string]Task
	events    map[string][]Event
	dedupKeys map[string]map[string]struct{}
}

func OpenJournal(path string) (*JournalStore, error) {
	store := &JournalStore{
		agents:    make(map[string]Agent),
		tasks:     make(map[string]Task),
		events:    make(map[string][]Event),
		dedupKeys: make(map[string]map[string]struct{}),
	}
	if path == "" {
		return store, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure journal permissions: %w", err)
	}
	store.file = file
	if err := store.replay(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func resourceKey(tenantID, id string) string {
	return tenantID + "\x00" + id
}

func (s *JournalStore) replay() error {
	if _, err := s.file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek journal: %w", err)
	}
	scanner := bufio.NewScanner(s.file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var record journalRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("decode journal line %d: %w", line, err)
		}
		if record.Version != 1 {
			return fmt.Errorf("unsupported journal version %d at line %d", record.Version, line)
		}
		s.applyRecord(record)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read journal: %w", err)
	}
	_, err := s.file.Seek(0, 2)
	return err
}

func (s *JournalStore) applyRecord(record journalRecord) {
	switch record.Kind {
	case "agent":
		if record.Agent != nil {
			s.agents[resourceKey(record.Agent.TenantID, record.Agent.ID)] = *record.Agent
		}
	case "task":
		if record.Task == nil {
			return
		}
		key := resourceKey(record.Task.TenantID, record.Task.ID)
		s.tasks[key] = *record.Task
		if record.Event != nil {
			s.events[key] = append(s.events[key], *record.Event)
			if s.dedupKeys[key] == nil {
				s.dedupKeys[key] = make(map[string]struct{})
			}
			s.dedupKeys[key][record.Event.DedupKey] = struct{}{}
		}
	}
}

func (s *JournalStore) append(record journalRecord) error {
	if s.file == nil {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := s.file.Write(encoded); err != nil {
		return fmt.Errorf("append journal record: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync journal record: %w", err)
	}
	return nil
}

func (s *JournalStore) PutAgent(_ context.Context, agent Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clone, err := CloneAgent(agent)
	if err != nil {
		return err
	}
	record := journalRecord{Version: 1, Kind: "agent", Agent: &clone}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) GetAgent(_ context.Context, tenantID, id string) (Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agent, ok := s.agents[resourceKey(tenantID, id)]
	if !ok {
		return Agent{}, ErrNotFound
	}
	return CloneAgent(agent)
}

func (s *JournalStore) ListAgents(_ context.Context, tenantID string) ([]Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	agents := make([]Agent, 0)
	for _, agent := range s.agents {
		if agent.TenantID == tenantID {
			clone, err := CloneAgent(agent)
			if err != nil {
				return nil, err
			}
			agents = append(agents, clone)
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents, nil
}

func (s *JournalStore) CreateTask(_ context.Context, task Task, event Event) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(task.TenantID, task.ID)
	if _, exists := s.tasks[key]; exists {
		return Task{}, ErrConflict
	}
	task.Revision = 1
	task.LastSequence = 1
	event.ID = NewID()
	event.TaskID = task.ID
	event.TenantID = task.TenantID
	event.Sequence = 1
	if event.DedupKey == "" {
		event.DedupKey = "local:" + event.ID
	}
	record := journalRecord{Version: 1, Kind: "task", Task: &task, Event: &event}
	if err := s.append(record); err != nil {
		return Task{}, err
	}
	s.applyRecord(record)
	return task, nil
}

func (s *JournalStore) ApplyTask(
	_ context.Context,
	tenantID string,
	id string,
	dedupKey string,
	mutate func(*Task) (Event, error),
) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(tenantID, id)
	current, ok := s.tasks[key]
	if !ok {
		return Task{}, false, ErrNotFound
	}
	if dedupKey != "" {
		if _, duplicate := s.dedupKeys[key][dedupKey]; duplicate {
			return current, false, nil
		}
	}
	updated, err := CloneTask(current)
	if err != nil {
		return Task{}, false, err
	}
	event, err := mutate(&updated)
	if err != nil {
		return Task{}, false, err
	}
	updated.Revision++
	updated.LastSequence++
	event.ID = NewID()
	event.TaskID = updated.ID
	event.TenantID = updated.TenantID
	event.Sequence = updated.LastSequence
	event.DedupKey = dedupKey
	if event.DedupKey == "" {
		event.DedupKey = "local:" + event.ID
	}
	record := journalRecord{Version: 1, Kind: "task", Task: &updated, Event: &event}
	if err := s.append(record); err != nil {
		return Task{}, false, err
	}
	s.applyRecord(record)
	return updated, true, nil
}

func (s *JournalStore) GetTask(_ context.Context, tenantID, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[resourceKey(tenantID, id)]
	if !ok {
		return Task{}, ErrNotFound
	}
	return CloneTask(task)
}

func (s *JournalStore) ListRecoverable(_ context.Context) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := make([]Task, 0)
	for _, task := range s.tasks {
		if !task.State.Terminal() && task.RemoteTaskID != "" {
			clone, err := CloneTask(task)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, clone)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

func (s *JournalStore) EventsAfter(_ context.Context, tenantID, id string, after uint64) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := resourceKey(tenantID, id)
	if _, ok := s.tasks[key]; !ok {
		return nil, ErrNotFound
	}
	result := make([]Event, 0)
	for _, event := range s.events[key] {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func (s *JournalStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}
