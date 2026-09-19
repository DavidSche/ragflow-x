package ragflow

import (
	"context"
	"testing"
)

func TestMockResourceGovernanceContract(t *testing.T) {
	ctx := context.Background()
	mock := NewMock()

	dataset, err := mock.CreateDataset(ctx, CreateDatasetRequest{Name: "Enterprise KB"})
	if err != nil || dataset.ID == "" || dataset.Name != "Enterprise KB" {
		t.Fatalf("create dataset: %+v err=%v", dataset, err)
	}
	if err := mock.DeleteDataset(ctx, "missing"); err == nil {
		t.Fatal("expected missing dataset error")
	}
	datasets, err := mock.ListDatasets(ctx)
	if err != nil || len(datasets) != 1 || datasets[0].ID != dataset.ID {
		t.Fatalf("list datasets: %+v err=%v", datasets, err)
	}
	document, err := mock.CreateDocument(ctx, dataset.ID, &DocumentUpload{Name: "handbook.md"})
	if err != nil || document.Name != "handbook.md" {
		t.Fatalf("create document: %+v err=%v", document, err)
	}
	if err := mock.ParseDocuments(ctx, dataset.ID, []string{document.ID}); err != nil {
		t.Fatal(err)
	}
	if err := mock.StopDocuments(ctx, dataset.ID, []string{document.ID}); err != nil {
		t.Fatal(err)
	}
	if err := mock.SetDocumentsStatus(ctx, dataset.ID, []string{document.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := mock.UpdateDocumentMetadata(ctx, dataset.ID, document.ID, map[string]interface{}{"owner": "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.UpdateDocumentMetadata(ctx, dataset.ID, "missing", nil); err == nil {
		t.Fatal("expected missing document error")
	}
	documents, err := mock.ListDocuments(ctx, dataset.ID)
	if err != nil || len(documents) != 1 || documents[0].Status != "parsed" {
		t.Fatalf("documents: %+v err=%v", documents, err)
	}
	mock.SeedDocumentChunk(dataset.ID, document.ID, "chunk-1", "first chunk", true)
	mock.SeedDocumentChunk(dataset.ID, document.ID, "chunk-2", "second chunk", false)
	chunks, total, err := mock.ListDocumentChunks(ctx, dataset.ID, document.ID, 1, 10)
	if err != nil || total != 2 || len(chunks) != 2 {
		t.Fatalf("chunks: %+v total=%d err=%v", chunks, total, err)
	}
	if _, _, err := mock.ListDocumentChunks(ctx, dataset.ID, document.ID, 0, 0); err == nil {
		t.Fatal("expected invalid pagination error")
	}
	if err := mock.SetChunksAvailable(ctx, dataset.ID, document.ID, []string{"chunk-2"}, true); err != nil {
		t.Fatal(err)
	}
	chunks, _, err = mock.ListDocumentChunks(ctx, dataset.ID, document.ID, 1, 10)
	if err != nil || !chunks[1].Available {
		t.Fatalf("enabled chunks: %+v err=%v", chunks, err)
	}
	if err := mock.DeleteChunks(ctx, dataset.ID, document.ID, []string{"chunk-2"}); err != nil {
		t.Fatal(err)
	}
	config, err := mock.GetDatasetConfig(ctx, dataset.ID)
	if err != nil || config.ID != dataset.ID {
		t.Fatalf("dataset config: %+v err=%v", config, err)
	}
	embedding := "qwen3-embedding-0.6b"
	if err := mock.UpdateDatasetConfig(ctx, dataset.ID, DatasetConfigUpdate{EmbeddingModel: &embedding}); err != nil {
		t.Fatal(err)
	}
	health, err := mock.Health(ctx)
	if err != nil || health.Status != "green" {
		t.Fatalf("mock health: %+v err=%v", health, err)
	}
	if err := mock.DeleteDocuments(ctx, dataset.ID, []string{document.ID}); err != nil {
		t.Fatal(err)
	}

	agent, err := mock.CreateAgent(ctx, CreateAgentRequest{
		Title: "Support", Dsl: map[string]interface{}{"version": 1}, Release: true,
	})
	if err != nil || agent.ID == "" {
		t.Fatalf("create agent: %+v err=%v", agent, err)
	}
	agents, total, err := mock.ListAgents(ctx, ListAgentsFilter{Page: 1, PageSize: 10})
	if err != nil || total != 1 || len(agents) != 1 || agents[0].Title != "Support" {
		t.Fatalf("agents: %+v total=%d err=%v", agents, total, err)
	}
	gotAgent, err := mock.GetAgent(ctx, agent.ID)
	if err != nil || gotAgent.Title != "Support" {
		t.Fatalf("get agent: %+v err=%v", gotAgent, err)
	}
	if _, err := mock.GetAgent(ctx, "missing"); err == nil {
		t.Fatal("expected missing agent error")
	}
	release := false
	if err := mock.UpdateAgent(ctx, agent.ID, UpdateAgentRequest{Release: &release}); err != nil {
		t.Fatalf("update agent err=%v", err)
	}
	if versions, err := mock.ListAgentVersions(ctx, agent.ID); err != nil || len(versions) != 1 {
		t.Fatalf("agent versions: %+v err=%v", versions, err)
	}
	versionID := ""
	if versions, _ := mock.ListAgentVersions(ctx, agent.ID); len(versions) > 0 {
		versionID = versions[0].ID
	}
	if versionID != "" {
		version, err := mock.GetAgentVersion(ctx, agent.ID, versionID)
		if err != nil || version.ID != versionID {
			t.Fatalf("agent version: %+v err=%v", version, err)
		}
		if err := mock.RollbackAgentVersion(ctx, agent.ID, versionID); err != nil {
			t.Fatal(err)
		}
	}
	agentSession, err := mock.CreateAgentSession(ctx, agent.ID, "Agent session")
	if err != nil || agentSession.ID == "" {
		t.Fatalf("agent session: %+v err=%v", agentSession, err)
	}
	if sessions, total, err := mock.ListAgentSessions(ctx, agent.ID, SessionListOptions{Page: 1, PageSize: 1}); err != nil || total != 1 || len(sessions) != 1 {
		t.Fatalf("agent sessions: %+v total=%d err=%v", sessions, total, err)
	}
	if messages, next, err := mock.ListAgentSessionMessages(ctx, agent.ID, agentSession.ID, SessionMessagePageOptions{Limit: 1}); err != nil || next != "" || len(messages) != 1 {
		t.Fatalf("agent session messages: %+v next=%s err=%v", messages, next, err)
	}

	chat, err := mock.CreateChat(ctx, CreateChatRequest{Name: "Support assistant", DatasetIDs: []string{dataset.ID}})
	if err != nil || chat.ID == "" {
		t.Fatalf("create chat: %+v err=%v", chat, err)
	}
	if chats, err := mock.ListChats(ctx); err != nil || len(chats) != 1 {
		t.Fatalf("chats: %+v err=%v", chats, err)
	}
	chatSession, err := mock.CreateChatSession(ctx, chat.ID, "Chat session")
	if err != nil || chatSession.ID == "" {
		t.Fatalf("chat session: %+v err=%v", chatSession, err)
	}
	if sessions, err := mock.ListChatSessions(ctx, chat.ID, SessionListOptions{Page: 1, PageSize: 10}); err != nil || len(sessions) != 1 {
		t.Fatal(err)
	}
	if _, _, err := mock.ListChatSessionMessages(ctx, chat.ID, chatSession.ID, SessionMessagePageOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.UpdateChatSession(ctx, chat.ID, chatSession.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := mock.DeleteChatSessions(ctx, chat.ID, []string{chatSession.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.UpdateChat(ctx, chat.ID, UpdateChatRequest{Name: "Renamed assistant"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.GetChat(ctx, "missing"); err == nil {
		t.Fatal("expected missing chat error")
	}
	if _, err := mock.GetChunk(ctx, chat.ID, "missing", "missing"); err == nil {
		t.Fatal("expected missing chunk error")
	}

	memoryID, err := mock.CreateMemory(ctx, CreateMemoryRequest{Name: "Enterprise memory", MemoryType: []string{"raw"}})
	if err != nil || memoryID == "" {
		t.Fatalf("memory create: %+v err=%v", memoryID, err)
	}
	if memories, total, err := mock.ListMemories(ctx, ListMemoriesFilter{Page: 1, PageSize: 10}); err != nil || total != 1 || len(memories) != 1 {
		t.Fatalf("memories: %+v total=%d err=%v", memories, total, err)
	}
	if _, err := mock.GetMemoryConfig(ctx, memoryID); err != nil {
		t.Fatal(err)
	}
	description := " governed memory"
	if _, err := mock.UpdateMemory(ctx, memoryID, UpdateMemoryRequest{Description: &description}); err != nil {
		t.Fatal(err)
	}
	if err := mock.DeleteMemory(ctx, memoryID); err != nil {
		t.Fatal(err)
	}
}
