package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type TaskState string

const (
	TaskStateUnknown       TaskState = "UNKNOWN"
	TaskStateSubmitted     TaskState = "SUBMITTED"
	TaskStateWorking       TaskState = "WORKING"
	TaskStateInputRequired TaskState = "INPUT_REQUIRED"
	TaskStateAuthRequired  TaskState = "AUTH_REQUIRED"
	TaskStateCompleted     TaskState = "COMPLETED"
	TaskStateFailed        TaskState = "FAILED"
	TaskStateCanceled      TaskState = "CANCELED"
	TaskStateRejected      TaskState = "REJECTED"
)

func (s TaskState) Terminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed ||
		s == TaskStateCanceled || s == TaskStateRejected
}

// WorkflowState is the Hub-owned aggregate state for a multi-Provider
// operation. Provider Tasks remain opaque children of this aggregate.
type WorkflowState string

const (
	WorkflowStatePending         WorkflowState = "PENDING"
	WorkflowStateRunning         WorkflowState = "RUNNING"
	WorkflowStateWaitingInput    WorkflowState = "WAITING_INPUT"
	WorkflowStateCompleted       WorkflowState = "COMPLETED"
	WorkflowStateFailed          WorkflowState = "FAILED"
	WorkflowStatePartiallyFailed WorkflowState = "PARTIALLY_FAILED"
	WorkflowStateCompensating    WorkflowState = "COMPENSATING"
	WorkflowStateCompensated     WorkflowState = "COMPENSATED"
	WorkflowStateCanceled        WorkflowState = "CANCELED"
)

func (s WorkflowState) Terminal() bool {
	return s == WorkflowStateCompleted || s == WorkflowStateFailed ||
		s == WorkflowStatePartiallyFailed || s == WorkflowStateCompensated ||
		s == WorkflowStateCanceled
}

type WorkflowStep struct {
	ID                  string    `json:"id"`
	AgentID             string    `json:"agentId"`
	Skill               string    `json:"skill,omitempty"`
	TaskID              string    `json:"taskId,omitempty"`
	State               TaskState `json:"state"`
	Required            bool      `json:"required"`
	CompensationText    string    `json:"compensationText,omitempty"`
	CompensationTaskID  string    `json:"compensationTaskId,omitempty"`
	CompensationState   TaskState `json:"compensationState,omitempty"`
	Problem             *Problem  `json:"problem,omitempty"`
	CompensationProblem *Problem  `json:"compensationProblem,omitempty"`
}

type Workflow struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenantId"`
	Name         string         `json:"name"`
	State        WorkflowState  `json:"state"`
	Steps        []WorkflowStep `json:"steps"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	Revision     uint64         `json:"revision"`
	LastSequence uint64         `json:"lastSequence"`
}

type WorkflowEvent struct {
	ID         string        `json:"id"`
	DedupKey   string        `json:"dedupKey"`
	WorkflowID string        `json:"workflowId"`
	TenantID   string        `json:"tenantId"`
	Sequence   uint64        `json:"sequence"`
	Type       string        `json:"type"`
	Source     string        `json:"source"`
	State      WorkflowState `json:"state,omitempty"`
	StepID     string        `json:"stepId,omitempty"`
	Problem    *Problem      `json:"problem,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
}

func CloneWorkflow(workflow Workflow) (Workflow, error) {
	encoded, err := json.Marshal(workflow)
	if err != nil {
		return Workflow{}, err
	}
	var clone Workflow
	err = json.Unmarshal(encoded, &clone)
	return clone, err
}

type DeliveryState string

const (
	DeliveryPending      DeliveryState = "PENDING"
	DeliveryAcknowledged DeliveryState = "ACKNOWLEDGED"
	DeliveryAmbiguous    DeliveryState = "AMBIGUOUS"
)

type PartKind string

const (
	PartText PartKind = "text"
	PartFile PartKind = "file"
	PartData PartKind = "data"
)

type Part struct {
	Kind        PartKind `json:"kind"`
	MediaType   string   `json:"mediaType,omitempty"`
	Filename    string   `json:"filename,omitempty"`
	Text        string   `json:"text,omitempty"`
	BytesBase64 string   `json:"bytesBase64,omitempty"`
	URI         string   `json:"uri,omitempty"`
	Data        any      `json:"data,omitempty"`
	ObjectID    string   `json:"objectId,omitempty"`
	SizeBytes   int64    `json:"sizeBytes,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
}

type Artifact struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Parts       []Part `json:"parts"`
	Complete    bool   `json:"complete"`
}

type Problem struct {
	Category  string `json:"category"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Ambiguous bool   `json:"ambiguous"`
}

type Agent struct {
	ID                    string            `json:"id"`
	TenantID              string            `json:"tenantId"`
	CardURL               string            `json:"cardUrl"`
	Name                  string            `json:"name"`
	ProviderVersion       string            `json:"providerVersion,omitempty"`
	ProtocolBinding       string            `json:"protocolBinding"`
	ProtocolVersion       string            `json:"protocolVersion"`
	Endpoint              string            `json:"endpoint"`
	Streaming             bool              `json:"streaming"`
	PushNotifications     bool              `json:"pushNotifications"`
	SecuritySchemes       []string          `json:"securitySchemes,omitempty"`
	CardSignatureVerified bool              `json:"cardSignatureVerified,omitempty"`
	CardSignatureKeyID    string            `json:"cardSignatureKeyId,omitempty"`
	Skills                []string          `json:"skills,omitempty"`
	CredentialEnv         map[string]string `json:"credentialEnv,omitempty"`
	RegistrationSource    string            `json:"registrationSource,omitempty"`
	RegistryEndpoint      string            `json:"registryEndpoint,omitempty"`
	LastRegistrySyncAt    *time.Time        `json:"lastRegistrySyncAt,omitempty"`
	HealthStatus          string            `json:"healthStatus,omitempty"`
	HealthMessage         string            `json:"healthMessage,omitempty"`
	LastHealthCheckAt     *time.Time        `json:"lastHealthCheckAt,omitempty"`
	CreatedAt             time.Time         `json:"createdAt"`
	UpdatedAt             time.Time         `json:"updatedAt"`
}

const (
	AgentHealthUnknown   = "UNKNOWN"
	AgentHealthHealthy   = "HEALTHY"
	AgentHealthUnhealthy = "UNHEALTHY"
	AgentHealthStale     = "STALE"
)

func CloneAgent(agent Agent) (Agent, error) {
	encoded, err := json.Marshal(agent)
	if err != nil {
		return Agent{}, err
	}
	var clone Agent
	err = json.Unmarshal(encoded, &clone)
	return clone, err
}

type Task struct {
	ID                   string        `json:"id"`
	TenantID             string        `json:"tenantId"`
	AgentID              string        `json:"agentId"`
	MessageID            string        `json:"messageId"`
	InputDigest          string        `json:"inputDigest"`
	RemoteTaskID         string        `json:"remoteTaskId,omitempty"`
	RemoteContextID      string        `json:"remoteContextId,omitempty"`
	State                TaskState     `json:"state"`
	Delivery             DeliveryState `json:"delivery"`
	CancelRequested      bool          `json:"cancelRequested"`
	PushTokenHash        string        `json:"pushTokenHash,omitempty"`
	Artifacts            []Artifact    `json:"artifacts,omitempty"`
	Problem              *Problem      `json:"problem,omitempty"`
	LastRemoteObservedAt *time.Time    `json:"lastRemoteObservedAt,omitempty"`
	CreatedAt            time.Time     `json:"createdAt"`
	UpdatedAt            time.Time     `json:"updatedAt"`
	Revision             uint64        `json:"revision"`
	LastSequence         uint64        `json:"lastSequence"`
}

type WorkLease struct {
	Task      Task      `json:"task"`
	Owner     string    `json:"owner"`
	ExpiresAt time.Time `json:"expiresAt"`
	Attempt   uint32    `json:"attempt"`
}

type InboxItem struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenantId"`
	TaskID    string          `json:"taskId"`
	DedupKey  string          `json:"dedupKey"`
	Protocol  string          `json:"protocol"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type InboxLease struct {
	Item      InboxItem `json:"item"`
	Owner     string    `json:"owner"`
	ExpiresAt time.Time `json:"expiresAt"`
	Attempt   uint32    `json:"attempt"`
}

// OutboxItem is a durable, at-least-once publication record derived from a
// committed Hub event. Consumers must use DedupKey when applying side effects.
type OutboxItem struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenantId"`
	TaskID    string          `json:"taskId"`
	DedupKey  string          `json:"dedupKey"`
	Topic     string          `json:"topic"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type OutboxLease struct {
	Item      OutboxItem `json:"item"`
	Owner     string     `json:"owner"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Attempt   uint32     `json:"attempt"`
}

type OutboxStatus string

const (
	OutboxPending      OutboxStatus = "PENDING"
	OutboxAcked        OutboxStatus = "ACKED"
	OutboxDeadLettered OutboxStatus = "DEAD_LETTERED"
	OutboxPurged       OutboxStatus = "PURGED"
)

// OutboxRecord is the operator-visible projection of a durable publication.
// Payload is retained so an authorized replay can reconstruct the same event.
type OutboxRecord struct {
	Item         OutboxItem   `json:"item"`
	Status       OutboxStatus `json:"status"`
	Attempts     uint32       `json:"attempts"`
	AvailableAt  time.Time    `json:"availableAt"`
	LastError    string       `json:"lastError,omitempty"`
	AckedAt      *time.Time   `json:"ackedAt,omitempty"`
	DeadLetterAt *time.Time   `json:"deadLetteredAt,omitempty"`
	PurgedAt     *time.Time   `json:"purgedAt,omitempty"`
}

type TokenRevocation struct {
	Issuer    string    `json:"issuer"`
	TokenID   string    `json:"tokenId"`
	TenantID  string    `json:"tenantId"`
	Reason    string    `json:"reason,omitempty"`
	RevokedAt time.Time `json:"revokedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ArtifactObjectStatus string

const (
	ArtifactObjectPending     ArtifactObjectStatus = "PENDING"
	ArtifactObjectAvailable   ArtifactObjectStatus = "AVAILABLE"
	ArtifactObjectQuarantined ArtifactObjectStatus = "QUARANTINED"
	ArtifactObjectFailed      ArtifactObjectStatus = "FAILED"
	ArtifactObjectDeleting    ArtifactObjectStatus = "DELETING"
	ArtifactObjectDeleted     ArtifactObjectStatus = "DELETED"
)

type ArtifactScanStatus string

const (
	ArtifactScanClean      ArtifactScanStatus = "CLEAN"
	ArtifactScanInfected   ArtifactScanStatus = "INFECTED"
	ArtifactScanError      ArtifactScanStatus = "ERROR"
	ArtifactScanNotScanned ArtifactScanStatus = "NOT_SCANNED"
)

type ArtifactObject struct {
	ID                string               `json:"id"`
	TenantID          string               `json:"tenantId"`
	TaskID            string               `json:"taskId"`
	ArtifactID        string               `json:"artifactId"`
	PartIndex         int                  `json:"partIndex"`
	StorageKey        string               `json:"-"`
	SHA256            string               `json:"sha256"`
	SizeBytes         int64                `json:"sizeBytes"`
	DeclaredMediaType string               `json:"declaredMediaType,omitempty"`
	DetectedMediaType string               `json:"detectedMediaType"`
	Filename          string               `json:"filename,omitempty"`
	SourceURI         string               `json:"sourceUri,omitempty"`
	Status            ArtifactObjectStatus `json:"status"`
	ScanStatus        ArtifactScanStatus   `json:"scanStatus"`
	CreatedAt         time.Time            `json:"createdAt"`
	UpdatedAt         time.Time            `json:"updatedAt"`
	ExpiresAt         time.Time            `json:"expiresAt"`
	DeletedAt         *time.Time           `json:"deletedAt,omitempty"`
	FailureCode       string               `json:"failureCode,omitempty"`
}

type ArtifactQuota struct {
	MaxBytes   int64 `json:"maxBytes"`
	MaxObjects int64 `json:"maxObjects"`
}

type ArtifactUsage struct {
	TenantID string `json:"tenantId"`
	Bytes    int64  `json:"bytes"`
	Objects  int64  `json:"objects"`
}

type ArtifactDeletionLease struct {
	Object    ArtifactObject `json:"object"`
	Owner     string         `json:"owner"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Attempt   uint32         `json:"attempt"`
}

type Event struct {
	ID         string     `json:"id"`
	DedupKey   string     `json:"dedupKey"`
	TaskID     string     `json:"taskId"`
	TenantID   string     `json:"tenantId"`
	Sequence   uint64     `json:"sequence"`
	Type       string     `json:"type"`
	Source     string     `json:"source"`
	State      TaskState  `json:"state,omitempty"`
	Artifact   *Artifact  `json:"artifact,omitempty"`
	Problem    *Problem   `json:"problem,omitempty"`
	ObservedAt *time.Time `json:"remoteObservedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func DigestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func DigestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return DigestString("unencodable")
	}
	return DigestString(string(encoded))
}

func CloneTask(task Task) (Task, error) {
	encoded, err := json.Marshal(task)
	if err != nil {
		return Task{}, err
	}
	var clone Task
	err = json.Unmarshal(encoded, &clone)
	return clone, err
}
