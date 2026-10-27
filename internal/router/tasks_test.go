package router_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestDeleteTasksOnlyRemovesTerminalTaskProjections(t *testing.T) {
	app := newTestApp(t)
	defer app.close()

	tasks := []*model.Task{
		{ID: "task-done", TenantID: "00000000000000000000000000000000", TaskType: model.TaskTypeUpload, Status: model.TaskStatusDone},
		{ID: "task-failed", TenantID: "00000000000000000000000000000000", TaskType: model.TaskTypeParse, Status: model.TaskStatusFailed},
		{ID: "task-running", TenantID: "00000000000000000000000000000000", TaskType: model.TaskTypeParse, Status: model.TaskStatusRunning},
	}
	for _, task := range tasks {
		if err := app.db.Create(task).Error; err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	token := app.login(t)

	payload, _ := json.Marshal(map[string][]string{"ids": {"task-done", "task-running"}})
	resp, body := app.doAuth(t, http.MethodDelete, "/api/v1/tasks", token, payload)
	if resp.StatusCode != 400 {
		t.Fatalf("mixed terminal/active delete: expected 400, got %d %s", resp.StatusCode, body)
	}

	payload, _ = json.Marshal(map[string][]string{"ids": {"task-done", "task-failed"}})
	resp, body = app.doAuth(t, http.MethodDelete, "/api/v1/tasks", token, payload)
	if resp.StatusCode != 200 {
		t.Fatalf("terminal delete: expected 200, got %d %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			Deleted int64 `json:"deleted"`
		} `json:"data"`
	}
	if err := decodeBody(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Deleted != 2 {
		t.Fatalf("expected 2 deleted tasks, got %d", out.Data.Deleted)
	}

	var remaining []model.Task
	if err := app.db.Order("id").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "task-running" {
		t.Fatalf("unexpected remaining tasks: %+v", remaining)
	}
}
