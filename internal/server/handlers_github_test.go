package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gogithub "github.com/google/go-github/v83/github"
	"github.com/jcwearn/agent-orchestrator/internal/coder"
	ghclient "github.com/jcwearn/agent-orchestrator/internal/github"
	"github.com/jcwearn/agent-orchestrator/internal/store"
)

const testWebhookSecret = "test-secret-123"

func testServerWithGitHub(t *testing.T, ghServerURL string) (*Server, *store.Store) {
	t.Helper()
	s := testStore(t)
	pool := coder.NewPool([]string{"agent-1", "agent-2"})
	exec := &mockExecutor{}
	hub := NewHub()

	gc := gogithub.NewClient(nil)
	gc, _ = gc.WithEnterpriseURLs(ghServerURL+"/", ghServerURL+"/")
	client := &ghclient.Client{Client: gc}

	srv := New(s, pool, exec, hub, slog.Default(), WithGitHub(client, []byte(testWebhookSecret)))
	return srv, s
}

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubWebhook_NotConfigured(t *testing.T) {
	// Server without GitHub configured.
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/v1/webhooks/github", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestGitHubWebhook_InvalidSignature(t *testing.T) {
	// Fake GitHub API server (we only need it for the client).
	ghServer := httptest.NewServer(http.NewServeMux())
	defer ghServer.Close()

	srv, _ := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestGitHubWebhook_IssuesLabeled_CreatesTask(t *testing.T) {
	// Fake GitHub server that records comment creation.
	var postedComment string
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("POST /api/v3/repos/testowner/testrepo/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		postedComment = body.Body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.IssueComment{
			ID:   gogithub.Ptr(int64(1)),
			Body: gogithub.Ptr(body.Body),
		})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// Build the webhook payload.
	event := gogithub.IssuesEvent{
		Action: gogithub.Ptr("labeled"),
		Label:  &gogithub.Label{Name: gogithub.Ptr("ai-task")},
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(42),
			Title:  gogithub.Ptr("Add caching layer"),
			Body:   gogithub.Ptr("We need Redis caching for the API."),
		},
		Repo: &gogithub.Repository{
			Name:          gogithub.Ptr("testrepo"),
			CloneURL:      gogithub.Ptr("https://github.com/testowner/testrepo.git"),
			DefaultBranch: gogithub.Ptr("main"),
			Owner:         &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify task was created in the store.
	tasks, err := s.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.SourceType != "github" {
		t.Fatalf("expected source_type 'github', got %q", task.SourceType)
	}
	if task.GithubOwner == nil || *task.GithubOwner != "testowner" {
		t.Fatalf("expected github_owner 'testowner', got %v", task.GithubOwner)
	}
	if task.GithubRepo == nil || *task.GithubRepo != "testrepo" {
		t.Fatalf("expected github_repo 'testrepo', got %v", task.GithubRepo)
	}
	if task.GithubIssue == nil || *task.GithubIssue != 42 {
		t.Fatalf("expected github_issue 42, got %v", task.GithubIssue)
	}
	if !strings.Contains(task.Prompt, "Add caching layer") {
		t.Fatalf("expected prompt to contain title, got %q", task.Prompt)
	}
	if !strings.Contains(task.Prompt, "Redis caching") {
		t.Fatalf("expected prompt to contain body, got %q", task.Prompt)
	}
	if task.RepoURL != "https://github.com/testowner/testrepo" {
		t.Fatalf("expected repo_url without .git suffix, got %q", task.RepoURL)
	}
	if task.BaseBranch != "main" {
		t.Fatalf("expected base_branch 'main', got %q", task.BaseBranch)
	}

	// Verify acknowledgement comment was posted.
	if postedComment == "" {
		t.Fatal("expected acknowledgement comment to be posted")
	}
	if !strings.Contains(postedComment, task.ID) {
		t.Fatal("acknowledgement should contain task ID")
	}
}

func TestGitHubWebhook_IssuesLabeled_WrongLabel(t *testing.T) {
	ghServer := httptest.NewServer(http.NewServeMux())
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	event := gogithub.IssuesEvent{
		Action: gogithub.Ptr("labeled"),
		Label:  &gogithub.Label{Name: gogithub.Ptr("bug")},
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(1),
			Title:  gogithub.Ptr("Some bug"),
		},
		Repo: &gogithub.Repository{
			Name:  gogithub.Ptr("testrepo"),
			Owner: &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// No task should be created.
	tasks, _ := s.ListTasks(context.Background(), "")
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGitHubWebhook_UnhandledEvent(t *testing.T) {
	ghServer := httptest.NewServer(http.NewServeMux())
	defer ghServer.Close()

	srv, _ := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	event := gogithub.PushEvent{
		Ref: gogithub.Ptr("refs/heads/main"),
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "push")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestGitHubWebhook_IssuesLabeled_TitleOnly(t *testing.T) {
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("POST /api/v3/repos/testowner/testrepo/issues/5/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.IssueComment{ID: gogithub.Ptr(int64(1))})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	event := gogithub.IssuesEvent{
		Action: gogithub.Ptr("labeled"),
		Label:  &gogithub.Label{Name: gogithub.Ptr("ai-task")},
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(5),
			Title:  gogithub.Ptr("Fix login bug"),
		},
		Repo: &gogithub.Repository{
			Name:     gogithub.Ptr("testrepo"),
			CloneURL: gogithub.Ptr("https://github.com/testowner/testrepo.git"),
			Owner:    &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify prompt is just the title (no body).
	tasks, _ := s.ListTasks(context.Background(), "")
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Prompt != "Fix login bug" {
		t.Fatalf("expected prompt to be just title, got %q", tasks[0].Prompt)
	}
}

func TestGitHubWebhook_IssuesOpened_WithAITaskLabel(t *testing.T) {
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("POST /api/v3/repos/testowner/testrepo/issues/10/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.IssueComment{ID: gogithub.Ptr(int64(1))})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	event := gogithub.IssuesEvent{
		Action: gogithub.Ptr("opened"),
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(10),
			Title:  gogithub.Ptr("Implement feature X"),
			Body:   gogithub.Ptr("Details about feature X."),
			Labels: []*gogithub.Label{
				{Name: gogithub.Ptr("ai-task")},
				{Name: gogithub.Ptr("enhancement")},
			},
		},
		Repo: &gogithub.Repository{
			Name:          gogithub.Ptr("testrepo"),
			CloneURL:      gogithub.Ptr("https://github.com/testowner/testrepo.git"),
			DefaultBranch: gogithub.Ptr("main"),
			Owner:         &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	tasks, err := s.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.SourceType != "github" {
		t.Fatalf("expected source_type 'github', got %q", task.SourceType)
	}
	if task.GithubIssue == nil || *task.GithubIssue != 10 {
		t.Fatalf("expected github_issue 10, got %v", task.GithubIssue)
	}
	if !strings.Contains(task.Prompt, "Implement feature X") {
		t.Fatalf("expected prompt to contain title, got %q", task.Prompt)
	}
}

func TestGitHubWebhook_DuplicateIssueEvent_Deduplicated(t *testing.T) {
	commentCount := 0
	ghMux := http.NewServeMux()
	ghMux.HandleFunc("POST /api/v3/repos/testowner/testrepo/issues/20/comments", func(w http.ResponseWriter, r *http.Request) {
		commentCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.IssueComment{ID: gogithub.Ptr(int64(1))})
	})
	ghServer := httptest.NewServer(ghMux)
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	// First event: "opened" with ai-task label — should create a task.
	openedEvent := gogithub.IssuesEvent{
		Action: gogithub.Ptr("opened"),
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(20),
			Title:  gogithub.Ptr("Dedup test"),
			Body:   gogithub.Ptr("Testing dedup."),
			Labels: []*gogithub.Label{{Name: gogithub.Ptr("ai-task")}},
		},
		Repo: &gogithub.Repository{
			Name:          gogithub.Ptr("testrepo"),
			CloneURL:      gogithub.Ptr("https://github.com/testowner/testrepo.git"),
			DefaultBranch: gogithub.Ptr("main"),
			Owner:         &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(openedEvent)
	sig := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for opened event, got %d", resp.StatusCode)
	}

	// Second event: "labeled" for the same issue — should be deduplicated.
	labeledEvent := gogithub.IssuesEvent{
		Action: gogithub.Ptr("labeled"),
		Label:  &gogithub.Label{Name: gogithub.Ptr("ai-task")},
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(20),
			Title:  gogithub.Ptr("Dedup test"),
			Body:   gogithub.Ptr("Testing dedup."),
			Labels: []*gogithub.Label{{Name: gogithub.Ptr("ai-task")}},
		},
		Repo: &gogithub.Repository{
			Name:          gogithub.Ptr("testrepo"),
			CloneURL:      gogithub.Ptr("https://github.com/testowner/testrepo.git"),
			DefaultBranch: gogithub.Ptr("main"),
			Owner:         &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload2, _ := json.Marshal(labeledEvent)
	sig2 := signPayload(payload2, testWebhookSecret)

	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload2)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Hub-Signature-256", sig2)
	req2.Header.Set("X-GitHub-Event", "issues")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for labeled event, got %d", resp2.StatusCode)
	}

	// Verify only one task was created.
	tasks, err := s.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task (dedup), got %d", len(tasks))
	}

	// Verify only one comment was posted.
	if commentCount != 1 {
		t.Fatalf("expected 1 comment, got %d", commentCount)
	}
}

func TestGitHubWebhook_IssuesOpened_WithoutAITaskLabel(t *testing.T) {
	ghServer := httptest.NewServer(http.NewServeMux())
	defer ghServer.Close()

	srv, s := testServerWithGitHub(t, ghServer.URL)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	event := gogithub.IssuesEvent{
		Action: gogithub.Ptr("opened"),
		Issue: &gogithub.Issue{
			Number: gogithub.Ptr(11),
			Title:  gogithub.Ptr("Regular issue"),
			Labels: []*gogithub.Label{
				{Name: gogithub.Ptr("bug")},
			},
		},
		Repo: &gogithub.Repository{
			Name:  gogithub.Ptr("testrepo"),
			Owner: &gogithub.User{Login: gogithub.Ptr("testowner")},
		},
	}
	payload, _ := json.Marshal(event)
	signature := signPayload(payload, testWebhookSecret)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/github", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "issues")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	tasks, _ := s.ListTasks(context.Background(), "")
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}
