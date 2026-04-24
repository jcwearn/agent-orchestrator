package github

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gogithub "github.com/google/go-github/v85/github"
)

// --- pure helper tests ---

func TestSplitComment(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		maxLen     int
		wantChunks int
		check      func(t *testing.T, chunks []string)
	}{
		{
			name:       "short body returns single chunk",
			body:       "Hello world",
			maxLen:     100,
			wantChunks: 1,
		},
		{
			name:       "exactly at limit returns single chunk",
			body:       strings.Repeat("a", 100),
			maxLen:     100,
			wantChunks: 1,
		},
		{
			name:       "splits at paragraph break",
			body:       "First paragraph\n\nSecond paragraph\n\nThird paragraph",
			maxLen:     40,
			wantChunks: 2,
			check: func(t *testing.T, chunks []string) {
				if chunks[0] != "First paragraph\n\nSecond paragraph" {
					t.Errorf("chunk 0 = %q", chunks[0])
				}
				if chunks[1] != "Third paragraph" {
					t.Errorf("chunk 1 = %q", chunks[1])
				}
			},
		},
		{
			name:       "splits at section header",
			body:       "## Section 1\n\nContent one.\n\n## Section 2\n\nContent two.",
			maxLen:     35,
			wantChunks: 2,
			check: func(t *testing.T, chunks []string) {
				if !strings.HasPrefix(chunks[0], "## Section 1") {
					t.Errorf("chunk 0 should start with section 1 header, got %q", chunks[0])
				}
			},
		},
		{
			name:       "all chunks within limit",
			body:       strings.Repeat("word ", 200),
			maxLen:     100,
			wantChunks: 0, // just check all fit
			check: func(t *testing.T, chunks []string) {
				for i, c := range chunks {
					if len(c) > 100 {
						t.Errorf("chunk %d length %d exceeds maxLen 100", i, len(c))
					}
				}
				origWords := strings.Fields(strings.Repeat("word ", 200))
				joinedWords := strings.Fields(strings.Join(chunks, "\n"))
				if len(origWords) != len(joinedWords) {
					t.Errorf("word count: got %d, want %d", len(joinedWords), len(origWords))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitComment(tt.body, tt.maxLen)
			if tt.wantChunks > 0 && len(chunks) != tt.wantChunks {
				t.Errorf("splitComment() returned %d chunks, want %d", len(chunks), tt.wantChunks)
			}
			if tt.check != nil {
				tt.check(t, chunks)
			}
		})
	}
}

func TestExtractCheckedDecisions(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no checkboxes",
			body: "Just some text\nwith no checkboxes",
			want: "",
		},
		{
			name: "unchecked checkboxes only",
			body: "- [ ] Option A\n- [ ] Option B",
			want: "",
		},
		{
			name: "one checked decision",
			body: "### Decision: DB\n- [x] PostgreSQL -- mature\n- [ ] SQLite -- simple",
			want: "- [x] PostgreSQL -- mature",
		},
		{
			name: "multiple checked decisions",
			body: "### Decision: DB\n- [x] PostgreSQL\n- [ ] SQLite\n### Decision: Cache\n- [ ] Redis\n- [x] Memcached",
			want: "- [x] PostgreSQL\n- [x] Memcached",
		},
		{
			name: "excludes Run tests checkbox",
			body: "- [x] Run tests before creating PR\n- [x] PostgreSQL",
			want: "- [x] PostgreSQL",
		},
		{
			name: "handles indented checkboxes",
			body: "  - [x] PostgreSQL\n  - [ ] SQLite",
			want: "- [x] PostgreSQL",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCheckedDecisions(tt.body)
			if got != tt.want {
				t.Errorf("extractCheckedDecisions() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusEmoji(t *testing.T) {
	if got := statusEmoji("Running"); got != "🔄" {
		t.Errorf("Running = %q", got)
	}
	if got := statusEmoji("Succeeded"); got != "✅" {
		t.Errorf("Succeeded = %q", got)
	}
	if got := statusEmoji("Failed"); got != "❌" {
		t.Errorf("Failed = %q", got)
	}
	if got := statusEmoji("Other"); got != "ℹ️" {
		t.Errorf("Other = %q", got)
	}
}

// --- httptest-based API tests ---

// fakeGitHub is a minimal test server that simulates GitHub API endpoints.
type fakeGitHub struct {
	mu       sync.Mutex
	comments []gogithub.IssueComment
	nextID   int64
	prBody   string // body of the fake PR
}

func newFakeGitHub() (*httptest.Server, *fakeGitHub) {
	fg := &fakeGitHub{nextID: 1}
	mux := http.NewServeMux()

	// Create comment
	mux.HandleFunc("POST /api/v3/repos/{owner}/{repo}/issues/{issue}/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		fg.mu.Lock()
		id := fg.nextID
		fg.nextID++
		comment := gogithub.IssueComment{
			ID:   gogithub.Ptr(id),
			Body: gogithub.Ptr(body.Body),
		}
		fg.comments = append(fg.comments, comment)
		fg.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(comment)
	})

	// List reactions on a comment
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/issues/comments/{comment_id}/reactions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]gogithub.Reaction{})
	})

	// List comments on an issue
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/issues/{issue}/comments", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		defer fg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fg.comments)
	})

	// Edit issue (for close)
	mux.HandleFunc("PATCH /api/v3/repos/{owner}/{repo}/issues/{issue}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.Issue{})
	})

	// Get pull request
	mux.HandleFunc("GET /api/v3/repos/{owner}/{repo}/pulls/{number}", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		body := fg.prBody
		fg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gogithub.PullRequest{Body: gogithub.Ptr(body)})
	})

	// Edit pull request
	mux.HandleFunc("PATCH /api/v3/repos/{owner}/{repo}/pulls/{number}", func(w http.ResponseWriter, r *http.Request) {
		var pr gogithub.PullRequest
		_ = json.NewDecoder(r.Body).Decode(&pr)
		fg.mu.Lock()
		fg.prBody = pr.GetBody()
		fg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pr)
	})

	ts := httptest.NewServer(mux)
	return ts, fg
}

func testNotifier(t *testing.T, serverURL string) *Notifier {
	t.Helper()
	gc := gogithub.NewClient(nil)
	gc, _ = gc.WithEnterpriseURLs(serverURL+"/", serverURL+"/")
	client := &Client{Client: gc}
	return NewNotifier(client, slog.Default())
}

func TestNotifyPlanReady_SingleComment(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	commentID, err := n.NotifyPlanReady(ctx, "owner", "repo", 1, "Short plan")
	if err != nil {
		t.Fatal(err)
	}
	if commentID == 0 {
		t.Fatal("expected non-zero comment ID")
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if len(fg.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(fg.comments))
	}
	body := fg.comments[0].GetBody()
	if !strings.Contains(body, "## Implementation Plan") {
		t.Error("comment should contain plan header")
	}
	if !strings.Contains(body, "Short plan") {
		t.Error("comment should contain plan text")
	}
	if !strings.Contains(body, "react with :+1:") {
		t.Error("comment should contain approval instructions")
	}
}

func TestNotifyPlanReady_MultipleComments(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	// Create a plan longer than the max comment length.
	longPlan := strings.Repeat("word ", 15000)
	commentID, err := n.NotifyPlanReady(ctx, "owner", "repo", 1, longPlan)
	if err != nil {
		t.Fatal(err)
	}
	if commentID == 0 {
		t.Fatal("expected non-zero comment ID")
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if len(fg.comments) < 2 {
		t.Fatalf("expected multiple comments for long plan, got %d", len(fg.comments))
	}
	// Last comment should have the approval footer.
	last := fg.comments[len(fg.comments)-1].GetBody()
	if !strings.Contains(last, "react with :+1:") {
		t.Error("last comment should contain approval instructions")
	}
	// First comment should have part header.
	first := fg.comments[0].GetBody()
	if !strings.Contains(first, "Part 1 of") {
		t.Error("first comment should have part header")
	}
}

func TestCheckApproval_NoReactionsNoComments(t *testing.T) {
	ts, _ := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	result, err := n.CheckApproval(ctx, "owner", "repo", 1, 999)
	if err != nil {
		t.Fatal(err)
	}
	if result.Approved {
		t.Error("expected not approved")
	}
	if result.Feedback != "" {
		t.Error("expected no feedback")
	}
}

func TestNotifyImplementationStarted(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.NotifyImplementationStarted(ctx, "owner", "repo", 1); err != nil {
		t.Fatal(err)
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if len(fg.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(fg.comments))
	}
	body := fg.comments[0].GetBody()
	if !strings.Contains(body, "Implementation Started") {
		t.Error("comment should contain implementation started header")
	}
	if !strings.Contains(body, "in progress") {
		t.Error("comment should contain in progress message")
	}
}

func TestNotifyComplete(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.NotifyComplete(ctx, "owner", "repo", 1, "https://github.com/owner/repo/pull/42"); err != nil {
		t.Fatal(err)
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if len(fg.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(fg.comments))
	}
	body := fg.comments[0].GetBody()
	if !strings.Contains(body, "Task Complete") {
		t.Error("comment should contain completion message")
	}
	if !strings.Contains(body, "pull/42") {
		t.Error("comment should contain PR URL")
	}
}

func TestNotifyFailed(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.NotifyFailed(ctx, "owner", "repo", 1, "SSH connection lost"); err != nil {
		t.Fatal(err)
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if len(fg.comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(fg.comments))
	}
	body := fg.comments[0].GetBody()
	if !strings.Contains(body, "Task Failed") {
		t.Error("comment should contain failure header")
	}
	if !strings.Contains(body, "SSH connection lost") {
		t.Error("comment should contain failure reason")
	}
}

func TestCloseIssue(t *testing.T) {
	ts, _ := newFakeGitHub()
	defer ts.Close()

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.CloseIssue(ctx, "owner", "repo", 1); err != nil {
		t.Fatal(err)
	}
}

func TestLinkPRToIssue_AppendClosingRef(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	fg.prBody = "Initial PR description"

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.LinkPRToIssue(ctx, "owner", "repo", 42, 7); err != nil {
		t.Fatal(err)
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	if !strings.Contains(fg.prBody, "Closes owner/repo#7") {
		t.Errorf("expected PR body to contain closing ref, got %q", fg.prBody)
	}
	if !strings.HasPrefix(fg.prBody, "Initial PR description") {
		t.Error("expected PR body to preserve original description")
	}
}

func TestLinkPRToIssue_Idempotent(t *testing.T) {
	ts, fg := newFakeGitHub()
	defer ts.Close()

	fg.prBody = "PR description\n\nCloses owner/repo#7"

	n := testNotifier(t, ts.URL)
	ctx := context.Background()

	if err := n.LinkPRToIssue(ctx, "owner", "repo", 42, 7); err != nil {
		t.Fatal(err)
	}

	fg.mu.Lock()
	defer fg.mu.Unlock()
	// Body should not be modified — no duplicate closing ref.
	if strings.Count(fg.prBody, "Closes owner/repo#7") != 1 {
		t.Errorf("expected exactly one closing ref, got body %q", fg.prBody)
	}
}

func TestContainsClosingKeyword(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		issueRef string
		want     bool
	}{
		{"closes present", "Closes owner/repo#5", "owner/repo#5", true},
		{"fixes present", "Fixes owner/repo#5", "owner/repo#5", true},
		{"resolves present", "Resolves owner/repo#5", "owner/repo#5", true},
		{"case insensitive", "closes owner/repo#5", "owner/repo#5", true},
		{"not present", "Some PR body", "owner/repo#5", false},
		{"different issue", "Closes owner/repo#3", "owner/repo#5", false},
		{"empty body", "", "owner/repo#5", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsClosingKeyword(tt.body, tt.issueRef)
			if got != tt.want {
				t.Errorf("containsClosingKeyword(%q, %q) = %v, want %v", tt.body, tt.issueRef, got, tt.want)
			}
		})
	}
}
