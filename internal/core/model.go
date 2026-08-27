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
	ID                string            `json:"id"`
	TenantID          string            `json:"tenantId"`
	CardURL           string            `json:"cardUrl"`
	Name              string            `json:"name"`
	ProviderVersion   string            `json:"providerVersion,omitempty"`
	ProtocolBinding   string            `json:"protocolBinding"`
	ProtocolVersion   string            `json:"protocolVersion"`
	Endpoint          string            `json:"endpoint"`
	Streaming         bool              `json:"streaming"`
	PushNotifications bool              `json:"pushNotifications"`
	SecuritySchemes   []string          `json:"securitySchemes,omitempty"`
	CredentialEnv     map[string]string `json:"credentialEnv,omitempty"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

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
