package main

import (
	"encoding/json"
	"testing"
)

func TestNormalizePushTaskIDAlias(t *testing.T) {
	for _, method := range []string{
		"CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig",
	} {
		body := []byte(`{"jsonrpc":"2.0","id":7,"method":"` + method + `","params":{"task_id":"task-1","id":"push-1"}}`)
		normalized := normalizePushTaskIDAlias(body)
		var request struct {
			ID     int                        `json:"id"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(normalized, &request); err != nil {
			t.Fatalf("%s normalized body is invalid: %v", method, err)
		}
		var taskID string
		if err := json.Unmarshal(request.Params["taskId"], &taskID); err != nil || taskID != "task-1" {
			t.Fatalf("%s taskId=%q err=%v", method, taskID, err)
		}
		if _, exists := request.Params["task_id"]; exists {
			t.Fatalf("%s retained snake_case task_id", method)
		}
		if request.ID != 7 {
			t.Fatalf("request ID=%d, want 7", request.ID)
		}
	}
}

func TestNormalizePushTaskIDAliasLeavesOtherMethodsUntouched(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"SendMessage","params":{"task_id":"task-1"}}`)
	normalized := normalizePushTaskIDAlias(body)
	if string(normalized) != string(body) {
		t.Fatalf("non-Push request changed: %s", normalized)
	}
}
