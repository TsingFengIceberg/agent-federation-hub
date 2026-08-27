package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound         = errors.New("resource not found")
	ErrConflict         = errors.New("resource already exists")
	ErrRevisionConflict = errors.New("resource revision changed")
	ErrLeaseLost        = errors.New("work lease is no longer owned")
	ErrQuotaExceeded    = errors.New("tenant artifact quota exceeded")
)

type Store interface {
	PutAgent(context.Context, Agent) error
	GetAgent(context.Context, string, string) (Agent, error)
	ListAgents(context.Context, string) ([]Agent, error)
	CreateTask(context.Context, Task, Event) (Task, error)
	ApplyTask(context.Context, string, string, string, func(*Task) (Event, error)) (Task, bool, error)
	ApplyTaskVersion(context.Context, string, string, uint64, string, func(*Task) (Event, error)) (Task, bool, error)
	GetTask(context.Context, string, string) (Task, error)
	ListRecoverable(context.Context) ([]Task, error)
	EventsAfter(context.Context, string, string, uint64) ([]Event, error)
	Close() error
}

type LeasedStore interface {
	Store
	ClaimRecoverable(context.Context, string, int, time.Time, time.Duration) ([]WorkLease, error)
	RenewLease(context.Context, WorkLease, time.Time, time.Duration) (WorkLease, error)
	ReleaseLease(context.Context, WorkLease, time.Time, bool) error
}

type InboxStore interface {
	EnqueueInbox(context.Context, InboxItem) (bool, error)
	ClaimInbox(context.Context, string, int, time.Time, time.Duration) ([]InboxLease, error)
	RenewInboxLease(context.Context, InboxLease, time.Time, time.Duration) (InboxLease, error)
	AckInbox(context.Context, InboxLease) error
	RetryInbox(context.Context, InboxLease, time.Time) error
}

// OutboxStore coordinates durable event publication across Hub instances.
// Delivery is intentionally at-least-once; publishers must be idempotent.
type OutboxStore interface {
	EnqueueOutbox(context.Context, OutboxItem) (bool, error)
	ClaimOutbox(context.Context, string, int, time.Time, time.Duration) ([]OutboxLease, error)
	RenewOutboxLease(context.Context, OutboxLease, time.Time, time.Duration) (OutboxLease, error)
	AckOutbox(context.Context, OutboxLease) error
	RetryOutbox(context.Context, OutboxLease, time.Time) error
	DeadLetterOutbox(context.Context, OutboxLease, string) error
}

type RevocationStore interface {
	RevokeToken(context.Context, TokenRevocation) error
	TokenRevoked(context.Context, string, string, string, time.Time) (bool, error)
	PruneRevocations(context.Context, time.Time) (int64, error)
}

type ArtifactMetadataStore interface {
	ReserveArtifact(context.Context, ArtifactObject, ArtifactQuota) (ArtifactObject, bool, error)
	FinalizeArtifact(context.Context, string, string, string, string, ArtifactScanStatus, ArtifactObjectStatus, time.Time) (ArtifactObject, error)
	FailArtifact(context.Context, string, string, string, time.Time) (ArtifactObject, error)
	GetArtifact(context.Context, string, string) (ArtifactObject, error)
	GetArtifactUsage(context.Context, string) (ArtifactUsage, error)
	ClaimExpiredArtifacts(context.Context, string, int, time.Time, time.Duration) ([]ArtifactDeletionLease, error)
	RenewArtifactLease(context.Context, ArtifactDeletionLease, time.Time, time.Duration) (ArtifactDeletionLease, error)
	CompleteArtifactDeletion(context.Context, ArtifactDeletionLease, time.Time) error
	RetryArtifactDeletion(context.Context, ArtifactDeletionLease, time.Time) error
}

type DurableStore interface {
	LeasedStore
	InboxStore
	OutboxStore
}

// HealthStore exposes a bounded dependency check for readiness probes. A
// successful health check means the store can accept and durably process new
// work; it is intentionally separate from Store so in-memory test doubles do
// not need to implement infrastructure-specific behavior.
type HealthStore interface {
	Health(context.Context) error
}

type leaseState struct {
	Owner       string    `json:"owner"`
	ExpiresAt   time.Time `json:"expiresAt"`
	AvailableAt time.Time `json:"availableAt"`
	Attempt     uint32    `json:"attempt"`
}

type inboxState struct {
	Owner       string    `json:"owner"`
	ExpiresAt   time.Time `json:"expiresAt"`
	AvailableAt time.Time `json:"availableAt"`
	Attempt     uint32    `json:"attempt"`
	Acked       bool      `json:"acked"`
}

type outboxState struct {
	Owner        string    `json:"owner"`
	ExpiresAt    time.Time `json:"expiresAt"`
	AvailableAt  time.Time `json:"availableAt"`
	Attempt      uint32    `json:"attempt"`
	Acked        bool      `json:"acked"`
	DeadLettered bool      `json:"deadLettered"`
	LastError    string    `json:"lastError,omitempty"`
}

type artifactLeaseState struct {
	Owner       string    `json:"owner"`
	ExpiresAt   time.Time `json:"expiresAt"`
	AvailableAt time.Time `json:"availableAt"`
	Attempt     uint32    `json:"attempt"`
}

type journalRecord struct {
	Version         int                 `json:"version"`
	Kind            string              `json:"kind"`
	Agent           *Agent              `json:"agent,omitempty"`
	Task            *Task               `json:"task,omitempty"`
	Event           *Event              `json:"event,omitempty"`
	TaskKey         string              `json:"taskKey,omitempty"`
	Lease           *leaseState         `json:"lease,omitempty"`
	Inbox           *InboxItem          `json:"inbox,omitempty"`
	InboxID         string              `json:"inboxId,omitempty"`
	InboxState      *inboxState         `json:"inboxState,omitempty"`
	Outbox          *OutboxItem         `json:"outbox,omitempty"`
	OutboxID        string              `json:"outboxId,omitempty"`
	OutboxState     *outboxState        `json:"outboxState,omitempty"`
	Revocation      *TokenRevocation    `json:"revocation,omitempty"`
	ArtifactObject  *ArtifactObject     `json:"artifactObject,omitempty"`
	ArtifactUsage   *ArtifactUsage      `json:"artifactUsage,omitempty"`
	ArtifactLeaseID string              `json:"artifactLeaseId,omitempty"`
	ArtifactLease   *artifactLeaseState `json:"artifactLease,omitempty"`
}

type JournalStore struct {
	mu              sync.RWMutex
	file            *os.File
	agents          map[string]Agent
	tasks           map[string]Task
	events          map[string][]Event
	dedupKeys       map[string]map[string]struct{}
	leases          map[string]leaseState
	inbox           map[string]InboxItem
	inboxDedup      map[string]string
	inboxStates     map[string]inboxState
	outbox          map[string]OutboxItem
	outboxDedup     map[string]string
	outboxStates    map[string]outboxState
	revocations     map[string]TokenRevocation
	artifactObjects map[string]ArtifactObject
	artifactUsage   map[string]ArtifactUsage
	artifactLeases  map[string]artifactLeaseState
}

// Backup writes a point-in-time copy of the append-only journal. The snapshot
// is fsynced and atomically renamed, so a restart can safely replay it even if
// the process is interrupted during the copy.
func (s *JournalStore) Backup(destination string) error {
	if s == nil || s.file == nil {
		return errors.New("journal backup requires a file-backed store")
	}
	if destination == "" {
		return errors.New("journal backup destination is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync journal before backup: %w", err)
	}
	info, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("stat journal before backup: %w", err)
	}
	destination = filepath.Clean(destination)
	if destinationInfo, statErr := os.Stat(destination); statErr == nil && os.SameFile(info, destinationInfo) {
		return errors.New("journal backup destination must differ from source")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create journal backup directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".journal-backup-*")
	if err != nil {
		return fmt.Errorf("create journal backup temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, io.NewSectionReader(s.file, 0, info.Size())); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("copy journal backup: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync journal backup: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("install journal backup: %w", err)
	}
	return nil
}

// RestoreJournalBackup validates a journal snapshot and atomically installs it
// as the new journal path. The destination must not be an open JournalStore;
// callers should perform restore while the Hub is stopped.
func RestoreJournalBackup(backup, destination string) error {
	if backup == "" || destination == "" {
		return errors.New("journal backup and destination are required")
	}
	backup = filepath.Clean(backup)
	destination = filepath.Clean(destination)
	if backup == destination {
		return errors.New("journal backup and destination must differ")
	}
	validated, err := OpenJournal(backup)
	if err != nil {
		return fmt.Errorf("validate journal backup: %w", err)
	}
	if err := validated.Close(); err != nil {
		return fmt.Errorf("close validated journal backup: %w", err)
	}
	source, err := os.Open(backup)
	if err != nil {
		return fmt.Errorf("open journal backup: %w", err)
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create journal restore directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".journal-restore-*")
	if err != nil {
		return fmt.Errorf("create journal restore temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, source); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("copy journal restore: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync journal restore: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("install journal restore: %w", err)
	}
	return nil
}

func OpenJournal(path string) (*JournalStore, error) {
	store := &JournalStore{
		agents:          make(map[string]Agent),
		tasks:           make(map[string]Task),
		events:          make(map[string][]Event),
		dedupKeys:       make(map[string]map[string]struct{}),
		leases:          make(map[string]leaseState),
		inbox:           make(map[string]InboxItem),
		inboxDedup:      make(map[string]string),
		inboxStates:     make(map[string]inboxState),
		outbox:          make(map[string]OutboxItem),
		outboxDedup:     make(map[string]string),
		outboxStates:    make(map[string]outboxState),
		revocations:     make(map[string]TokenRevocation),
		artifactObjects: make(map[string]ArtifactObject),
		artifactUsage:   make(map[string]ArtifactUsage),
		artifactLeases:  make(map[string]artifactLeaseState),
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
		if record.Outbox != nil {
			s.outbox[record.Outbox.ID] = *record.Outbox
			s.outboxDedup[outboxDedupKey(*record.Outbox)] = record.Outbox.ID
		}
	case "lease":
		if record.TaskKey != "" && record.Lease != nil {
			s.leases[record.TaskKey] = *record.Lease
		}
	case "inbox":
		if record.Inbox != nil {
			s.inbox[record.Inbox.ID] = *record.Inbox
			s.inboxDedup[inboxDedupKey(*record.Inbox)] = record.Inbox.ID
		}
	case "inbox_state":
		if record.InboxID != "" && record.InboxState != nil {
			s.inboxStates[record.InboxID] = *record.InboxState
		}
	case "outbox":
		if record.Outbox != nil {
			s.outbox[record.Outbox.ID] = *record.Outbox
			s.outboxDedup[outboxDedupKey(*record.Outbox)] = record.Outbox.ID
		}
	case "outbox_state":
		if record.OutboxID != "" && record.OutboxState != nil {
			s.outboxStates[record.OutboxID] = *record.OutboxState
		}
	case "revocation":
		if record.Revocation != nil {
			s.revocations[revocationKey(record.Revocation.Issuer, record.Revocation.TokenID, record.Revocation.TenantID)] = *record.Revocation
		}
	case "artifact":
		if record.ArtifactObject != nil {
			s.artifactObjects[resourceKey(record.ArtifactObject.TenantID, record.ArtifactObject.ID)] = *record.ArtifactObject
		}
		if record.ArtifactUsage != nil {
			s.artifactUsage[record.ArtifactUsage.TenantID] = *record.ArtifactUsage
		}
		if record.ArtifactLeaseID != "" && record.ArtifactLease != nil {
			s.artifactLeases[record.ArtifactLeaseID] = *record.ArtifactLease
		}
	case "artifact_lease":
		if record.ArtifactLeaseID != "" && record.ArtifactLease != nil {
			s.artifactLeases[record.ArtifactLeaseID] = *record.ArtifactLease
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
	outbox, err := outboxFromEvent(event)
	if err != nil {
		return Task{}, err
	}
	record := journalRecord{Version: 1, Kind: "task", Task: &task, Event: &event, Outbox: &outbox}
	if err := s.append(record); err != nil {
		return Task{}, err
	}
	s.applyRecord(record)
	return task, nil
}

func (s *JournalStore) ApplyTask(
	ctx context.Context,
	tenantID string,
	id string,
	dedupKey string,
	mutate func(*Task) (Event, error),
) (Task, bool, error) {
	return s.ApplyTaskVersion(ctx, tenantID, id, 0, dedupKey, mutate)
}

func (s *JournalStore) ApplyTaskVersion(
	_ context.Context,
	tenantID string,
	id string,
	expectedRevision uint64,
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
	if expectedRevision != 0 && current.Revision != expectedRevision {
		return current, false, ErrRevisionConflict
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
	outbox, err := outboxFromEvent(event)
	if err != nil {
		return Task{}, false, err
	}
	record := journalRecord{Version: 1, Kind: "task", Task: &updated, Event: &event, Outbox: &outbox}
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

func (s *JournalStore) ClaimRecoverable(
	_ context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]WorkLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("lease owner, positive limit, and duration are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.tasks))
	for key, task := range s.tasks {
		lease := s.leases[key]
		if task.State.Terminal() || task.RemoteTaskID == "" || lease.AvailableAt.After(now) ||
			(lease.Owner != "" && lease.ExpiresAt.After(now)) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]WorkLease, 0, len(keys))
	for _, key := range keys {
		state := s.leases[key]
		state.Owner = owner
		state.ExpiresAt = now.Add(duration)
		state.Attempt++
		record := journalRecord{Version: 1, Kind: "lease", TaskKey: key, Lease: &state}
		if err := s.append(record); err != nil {
			return nil, err
		}
		s.applyRecord(record)
		task, err := CloneTask(s.tasks[key])
		if err != nil {
			return nil, err
		}
		result = append(result, WorkLease{Task: task, Owner: owner, ExpiresAt: state.ExpiresAt, Attempt: state.Attempt})
	}
	return result, nil
}

func (s *JournalStore) RenewLease(
	_ context.Context,
	lease WorkLease,
	now time.Time,
	duration time.Duration,
) (WorkLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(lease.Task.TenantID, lease.Task.ID)
	state, ok := s.leases[key]
	if !ok || state.Owner != lease.Owner || !state.ExpiresAt.After(now) {
		return WorkLease{}, ErrLeaseLost
	}
	state.ExpiresAt = now.Add(duration)
	record := journalRecord{Version: 1, Kind: "lease", TaskKey: key, Lease: &state}
	if err := s.append(record); err != nil {
		return WorkLease{}, err
	}
	s.applyRecord(record)
	lease.ExpiresAt = state.ExpiresAt
	return lease, nil
}

func (s *JournalStore) ReleaseLease(
	_ context.Context,
	lease WorkLease,
	availableAt time.Time,
	resetAttempts bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(lease.Task.TenantID, lease.Task.ID)
	state, ok := s.leases[key]
	if !ok || state.Owner != lease.Owner {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.AvailableAt = availableAt
	if resetAttempts {
		state.Attempt = 0
	}
	record := journalRecord{Version: 1, Kind: "lease", TaskKey: key, Lease: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func inboxDedupKey(item InboxItem) string {
	return item.TenantID + "\x00" + item.TaskID + "\x00" + item.DedupKey
}

func outboxDedupKey(item OutboxItem) string {
	return item.TenantID + "\x00" + item.TaskID + "\x00" + item.DedupKey
}

func outboxFromEvent(event Event) (OutboxItem, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return OutboxItem{}, fmt.Errorf("encode outbox event: %w", err)
	}
	return OutboxItem{
		ID: event.ID, TenantID: event.TenantID, TaskID: event.TaskID,
		DedupKey: event.DedupKey, Topic: event.Type, Payload: payload,
		CreatedAt: event.CreatedAt,
	}, nil
}

func (s *JournalStore) EnqueueInbox(_ context.Context, item InboxItem) (bool, error) {
	if item.ID == "" || item.TenantID == "" || item.TaskID == "" || item.DedupKey == "" {
		return false, errors.New("inbox ID, tenant, Task, and dedup key are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, duplicate := s.inboxDedup[inboxDedupKey(item)]; duplicate {
		return false, nil
	}
	record := journalRecord{Version: 1, Kind: "inbox", Inbox: &item}
	if err := s.append(record); err != nil {
		return false, err
	}
	s.applyRecord(record)
	return true, nil
}

func (s *JournalStore) ClaimInbox(
	_ context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]InboxLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("inbox lease owner, positive limit, and duration are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.inbox))
	for id := range s.inbox {
		state := s.inboxStates[id]
		if state.Acked || state.AvailableAt.After(now) || (state.Owner != "" && state.ExpiresAt.After(now)) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	result := make([]InboxLease, 0, len(ids))
	for _, id := range ids {
		state := s.inboxStates[id]
		state.Owner = owner
		state.ExpiresAt = now.Add(duration)
		state.Attempt++
		record := journalRecord{Version: 1, Kind: "inbox_state", InboxID: id, InboxState: &state}
		if err := s.append(record); err != nil {
			return nil, err
		}
		s.applyRecord(record)
		result = append(result, InboxLease{Item: s.inbox[id], Owner: owner, ExpiresAt: state.ExpiresAt, Attempt: state.Attempt})
	}
	return result, nil
}

func (s *JournalStore) RenewInboxLease(
	_ context.Context,
	lease InboxLease,
	now time.Time,
	duration time.Duration,
) (InboxLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.inboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || !state.ExpiresAt.After(now) || state.Acked {
		return InboxLease{}, ErrLeaseLost
	}
	state.ExpiresAt = now.Add(duration)
	record := journalRecord{Version: 1, Kind: "inbox_state", InboxID: lease.Item.ID, InboxState: &state}
	if err := s.append(record); err != nil {
		return InboxLease{}, err
	}
	s.applyRecord(record)
	lease.ExpiresAt = state.ExpiresAt
	return lease, nil
}

func (s *JournalStore) AckInbox(_ context.Context, lease InboxLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.inboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || state.Acked {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.Acked = true
	record := journalRecord{Version: 1, Kind: "inbox_state", InboxID: lease.Item.ID, InboxState: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) RetryInbox(_ context.Context, lease InboxLease, availableAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.inboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || state.Acked {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.AvailableAt = availableAt
	record := journalRecord{Version: 1, Kind: "inbox_state", InboxID: lease.Item.ID, InboxState: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) EnqueueOutbox(_ context.Context, item OutboxItem) (bool, error) {
	if item.ID == "" || item.TenantID == "" || item.TaskID == "" || item.DedupKey == "" || item.Topic == "" {
		return false, errors.New("outbox ID, tenant, Task, dedup key, and topic are required")
	}
	if len(item.Payload) == 0 || !json.Valid(item.Payload) {
		return false, errors.New("outbox payload must be valid JSON")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, duplicate := s.outboxDedup[outboxDedupKey(item)]; duplicate {
		return false, nil
	}
	record := journalRecord{Version: 1, Kind: "outbox", Outbox: &item}
	if err := s.append(record); err != nil {
		return false, err
	}
	s.applyRecord(record)
	return true, nil
}

func (s *JournalStore) ClaimOutbox(
	_ context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]OutboxLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("outbox lease owner, positive limit, and duration are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.outbox))
	for id := range s.outbox {
		state := s.outboxStates[id]
		if state.Acked || state.DeadLettered || state.AvailableAt.After(now) || (state.Owner != "" && state.ExpiresAt.After(now)) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return s.outbox[ids[i]].CreatedAt.Before(s.outbox[ids[j]].CreatedAt) ||
			(s.outbox[ids[i]].CreatedAt.Equal(s.outbox[ids[j]].CreatedAt) && ids[i] < ids[j])
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	result := make([]OutboxLease, 0, len(ids))
	for _, id := range ids {
		state := s.outboxStates[id]
		state.Owner = owner
		state.ExpiresAt = now.Add(duration)
		state.Attempt++
		record := journalRecord{Version: 1, Kind: "outbox_state", OutboxID: id, OutboxState: &state}
		if err := s.append(record); err != nil {
			return nil, err
		}
		s.applyRecord(record)
		result = append(result, OutboxLease{Item: s.outbox[id], Owner: owner, ExpiresAt: state.ExpiresAt, Attempt: state.Attempt})
	}
	return result, nil
}

func (s *JournalStore) RenewOutboxLease(
	_ context.Context,
	lease OutboxLease,
	now time.Time,
	duration time.Duration,
) (OutboxLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.outboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || !state.ExpiresAt.After(now) || state.Acked {
		return OutboxLease{}, ErrLeaseLost
	}
	state.ExpiresAt = now.Add(duration)
	record := journalRecord{Version: 1, Kind: "outbox_state", OutboxID: lease.Item.ID, OutboxState: &state}
	if err := s.append(record); err != nil {
		return OutboxLease{}, err
	}
	s.applyRecord(record)
	lease.ExpiresAt = state.ExpiresAt
	return lease, nil
}

func (s *JournalStore) AckOutbox(_ context.Context, lease OutboxLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.outboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || state.Acked {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.Acked = true
	record := journalRecord{Version: 1, Kind: "outbox_state", OutboxID: lease.Item.ID, OutboxState: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) RetryOutbox(_ context.Context, lease OutboxLease, availableAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.outboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || state.Acked || state.DeadLettered {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.AvailableAt = availableAt
	record := journalRecord{Version: 1, Kind: "outbox_state", OutboxID: lease.Item.ID, OutboxState: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) DeadLetterOutbox(_ context.Context, lease OutboxLease, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.outboxStates[lease.Item.ID]
	if !ok || state.Owner != lease.Owner || state.Acked || state.DeadLettered {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.DeadLettered = true
	state.LastError = reason
	record := journalRecord{Version: 1, Kind: "outbox_state", OutboxID: lease.Item.ID, OutboxState: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func revocationKey(issuer, tokenID, tenantID string) string {
	return issuer + "\x00" + tokenID + "\x00" + tenantID
}

func (s *JournalStore) RevokeToken(_ context.Context, revocation TokenRevocation) error {
	if revocation.Issuer == "" || revocation.TokenID == "" || revocation.TenantID == "" ||
		revocation.RevokedAt.IsZero() || revocation.ExpiresAt.IsZero() {
		return errors.New("revocation issuer, token ID, tenant, revoked time, and expiry are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record := journalRecord{Version: 1, Kind: "revocation", Revocation: &revocation}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) TokenRevoked(
	_ context.Context,
	issuer string,
	tokenID string,
	tenantID string,
	now time.Time,
) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	revocation, ok := s.revocations[revocationKey(issuer, tokenID, tenantID)]
	return ok && now.Before(revocation.ExpiresAt), nil
}

func (s *JournalStore) PruneRevocations(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var removed int64
	for key, revocation := range s.revocations {
		if !now.Before(revocation.ExpiresAt) {
			delete(s.revocations, key)
			removed++
		}
	}
	// Journal compaction remains separate; expired decisions no longer affect
	// authentication even though their historical records remain append-only.
	return removed, nil
}

func validateArtifactReservation(object ArtifactObject) error {
	if object.ID == "" || object.TenantID == "" || object.TaskID == "" || object.ArtifactID == "" ||
		object.SHA256 == "" || object.SizeBytes < 0 || object.ExpiresAt.IsZero() {
		return errors.New("artifact ID, tenant, Task, Artifact, digest, non-negative size, and expiry are required")
	}
	return nil
}

func sameArtifactIdentity(first, second ArtifactObject) bool {
	return first.TenantID == second.TenantID && first.TaskID == second.TaskID &&
		first.ArtifactID == second.ArtifactID && first.PartIndex == second.PartIndex &&
		first.SHA256 == second.SHA256 && first.SizeBytes == second.SizeBytes
}

func (s *JournalStore) ReserveArtifact(
	_ context.Context,
	object ArtifactObject,
	quota ArtifactQuota,
) (ArtifactObject, bool, error) {
	if err := validateArtifactReservation(object); err != nil {
		return ArtifactObject{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(object.TenantID, object.ID)
	if existing, ok := s.artifactObjects[key]; ok {
		if !sameArtifactIdentity(existing, object) {
			return ArtifactObject{}, false, ErrConflict
		}
		if existing.Status != ArtifactObjectFailed {
			return existing, false, nil
		}
	}
	usage := s.artifactUsage[object.TenantID]
	usage.TenantID = object.TenantID
	if (quota.MaxBytes > 0 && usage.Bytes+object.SizeBytes > quota.MaxBytes) ||
		(quota.MaxObjects > 0 && usage.Objects+1 > quota.MaxObjects) {
		return ArtifactObject{}, false, ErrQuotaExceeded
	}
	object.Status = ArtifactObjectPending
	object.ScanStatus = ArtifactScanNotScanned
	object.StorageKey = ""
	object.FailureCode = ""
	object.DeletedAt = nil
	usage.Bytes += object.SizeBytes
	usage.Objects++
	record := journalRecord{Version: 1, Kind: "artifact", ArtifactObject: &object, ArtifactUsage: &usage}
	if err := s.append(record); err != nil {
		return ArtifactObject{}, false, err
	}
	s.applyRecord(record)
	return object, true, nil
}

func (s *JournalStore) FinalizeArtifact(
	_ context.Context,
	tenantID, id, storageKey, detectedMediaType string,
	scanStatus ArtifactScanStatus,
	status ArtifactObjectStatus,
	now time.Time,
) (ArtifactObject, error) {
	if storageKey == "" || (status != ArtifactObjectAvailable && status != ArtifactObjectQuarantined) {
		return ArtifactObject{}, errors.New("storage key and a final available or quarantined status are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(tenantID, id)
	object, ok := s.artifactObjects[key]
	if !ok {
		return ArtifactObject{}, ErrNotFound
	}
	if object.Status != ArtifactObjectPending {
		if object.Status == status && object.StorageKey == storageKey {
			return object, nil
		}
		return ArtifactObject{}, ErrConflict
	}
	object.StorageKey = storageKey
	object.DetectedMediaType = detectedMediaType
	object.ScanStatus = scanStatus
	object.Status = status
	object.UpdatedAt = now
	record := journalRecord{Version: 1, Kind: "artifact", ArtifactObject: &object}
	if err := s.append(record); err != nil {
		return ArtifactObject{}, err
	}
	s.applyRecord(record)
	return object, nil
}

func (s *JournalStore) FailArtifact(
	_ context.Context,
	tenantID, id, failureCode string,
	now time.Time,
) (ArtifactObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(tenantID, id)
	object, ok := s.artifactObjects[key]
	if !ok {
		return ArtifactObject{}, ErrNotFound
	}
	if object.Status == ArtifactObjectFailed {
		return object, nil
	}
	if object.Status != ArtifactObjectPending {
		return ArtifactObject{}, ErrConflict
	}
	usage := s.artifactUsage[tenantID]
	usage.Bytes -= object.SizeBytes
	usage.Objects--
	if usage.Bytes < 0 || usage.Objects < 0 {
		return ArtifactObject{}, errors.New("artifact usage invariant violated")
	}
	object.Status = ArtifactObjectFailed
	object.FailureCode = failureCode
	object.UpdatedAt = now
	record := journalRecord{Version: 1, Kind: "artifact", ArtifactObject: &object, ArtifactUsage: &usage}
	if err := s.append(record); err != nil {
		return ArtifactObject{}, err
	}
	s.applyRecord(record)
	return object, nil
}

func (s *JournalStore) GetArtifact(_ context.Context, tenantID, id string) (ArtifactObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.artifactObjects[resourceKey(tenantID, id)]
	if !ok {
		return ArtifactObject{}, ErrNotFound
	}
	return object, nil
}

func (s *JournalStore) GetArtifactUsage(_ context.Context, tenantID string) (ArtifactUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	usage := s.artifactUsage[tenantID]
	usage.TenantID = tenantID
	return usage, nil
}

func (s *JournalStore) ClaimExpiredArtifacts(
	_ context.Context,
	owner string,
	limit int,
	now time.Time,
	duration time.Duration,
) ([]ArtifactDeletionLease, error) {
	if owner == "" || limit <= 0 || duration <= 0 {
		return nil, errors.New("artifact lease owner, positive limit, and duration are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0)
	for key, object := range s.artifactObjects {
		lease := s.artifactLeases[key]
		eligibleStatus := object.Status == ArtifactObjectAvailable || object.Status == ArtifactObjectQuarantined ||
			object.Status == ArtifactObjectDeleting
		if !eligibleStatus || object.ExpiresAt.After(now) || lease.AvailableAt.After(now) ||
			(lease.Owner != "" && lease.ExpiresAt.After(now)) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]ArtifactDeletionLease, 0, len(keys))
	for _, key := range keys {
		object := s.artifactObjects[key]
		object.Status = ArtifactObjectDeleting
		object.UpdatedAt = now
		lease := s.artifactLeases[key]
		lease.Owner = owner
		lease.ExpiresAt = now.Add(duration)
		lease.Attempt++
		record := journalRecord{
			Version: 1, Kind: "artifact", ArtifactObject: &object,
			ArtifactLeaseID: key, ArtifactLease: &lease,
		}
		if err := s.append(record); err != nil {
			return nil, err
		}
		s.applyRecord(record)
		result = append(result, ArtifactDeletionLease{Object: object, Owner: owner, ExpiresAt: lease.ExpiresAt, Attempt: lease.Attempt})
	}
	return result, nil
}

func (s *JournalStore) RenewArtifactLease(
	_ context.Context,
	lease ArtifactDeletionLease,
	now time.Time,
	duration time.Duration,
) (ArtifactDeletionLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(lease.Object.TenantID, lease.Object.ID)
	state, ok := s.artifactLeases[key]
	if !ok || state.Owner != lease.Owner || !state.ExpiresAt.After(now) {
		return ArtifactDeletionLease{}, ErrLeaseLost
	}
	state.ExpiresAt = now.Add(duration)
	record := journalRecord{Version: 1, Kind: "artifact_lease", ArtifactLeaseID: key, ArtifactLease: &state}
	if err := s.append(record); err != nil {
		return ArtifactDeletionLease{}, err
	}
	s.applyRecord(record)
	lease.ExpiresAt = state.ExpiresAt
	return lease, nil
}

func (s *JournalStore) CompleteArtifactDeletion(
	_ context.Context,
	lease ArtifactDeletionLease,
	now time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(lease.Object.TenantID, lease.Object.ID)
	state, ok := s.artifactLeases[key]
	object, objectOK := s.artifactObjects[key]
	if !ok || !objectOK || state.Owner != lease.Owner || object.Status != ArtifactObjectDeleting {
		return ErrLeaseLost
	}
	usage := s.artifactUsage[object.TenantID]
	usage.Bytes -= object.SizeBytes
	usage.Objects--
	if usage.Bytes < 0 || usage.Objects < 0 {
		return errors.New("artifact usage invariant violated")
	}
	object.Status = ArtifactObjectDeleted
	object.UpdatedAt = now
	object.DeletedAt = &now
	state = artifactLeaseState{}
	record := journalRecord{
		Version: 1, Kind: "artifact", ArtifactObject: &object, ArtifactUsage: &usage,
		ArtifactLeaseID: key, ArtifactLease: &state,
	}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
}

func (s *JournalStore) RetryArtifactDeletion(
	_ context.Context,
	lease ArtifactDeletionLease,
	availableAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := resourceKey(lease.Object.TenantID, lease.Object.ID)
	state, ok := s.artifactLeases[key]
	if !ok || state.Owner != lease.Owner {
		return ErrLeaseLost
	}
	state.Owner = ""
	state.ExpiresAt = time.Time{}
	state.AvailableAt = availableAt
	record := journalRecord{Version: 1, Kind: "artifact_lease", ArtifactLeaseID: key, ArtifactLease: &state}
	if err := s.append(record); err != nil {
		return err
	}
	s.applyRecord(record)
	return nil
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

func (s *JournalStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.file == nil {
		return nil
	}
	if _, err := s.file.Stat(); err != nil {
		return fmt.Errorf("stat journal: %w", err)
	}
	return nil
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
