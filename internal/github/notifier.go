package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	gogithub "github.com/google/go-github/v83/github"
)

// Notifier posts status updates to GitHub issues.
type Notifier struct {
	client *Client
	logger *slog.Logger
}

// NewNotifier creates a new GitHub notifier.
func NewNotifier(client *Client, logger *slog.Logger) *Notifier {
	return &Notifier{client: client, logger: logger}
}

// ApprovalResult holds the outcome of checking a plan comment for approval.
type ApprovalResult struct {
	Approved  bool
	RunTests  bool
	Decisions string
	Feedback  string
}

// NotifyPlanReady posts the generated plan as a comment and returns the comment ID.
// If the plan is too long for a single GitHub comment, it splits across multiple
// comments with part headers. The returned comment ID is always the last comment
// (the one with the approval footer), so CheckApproval works correctly.
func (n *Notifier) NotifyPlanReady(ctx context.Context, owner, repo string, issue int, plan string) (int64, error) {
	footer := "\n\n---\n\n- [ ] Run tests before creating PR\n\n**To approve this plan**, react with :+1: on this comment.\n**To request changes**, reply to this issue with your feedback."

	// Reserve space for the header and footer in the size budget.
	// Header "## Implementation Plan (Part X of Y)\n\n" is ~45 chars max.
	chunks := splitComment(plan, maxCommentLen-len(footer)-50)

	if len(chunks) == 1 {
		body := fmt.Sprintf("## Implementation Plan\n\n%s%s", chunks[0], footer)
		comment, _, err := n.client.Issues.CreateComment(ctx, owner, repo, issue,
			&gogithub.IssueComment{Body: gogithub.Ptr(body)})
		if err != nil {
			return 0, err
		}
		return comment.GetID(), nil
	}

	// Multiple chunks — post each as a separate comment.
	var lastID int64
	for i, chunk := range chunks {
		var body string
		if i < len(chunks)-1 {
			body = fmt.Sprintf("## Implementation Plan (Part %d of %d)\n\n%s", i+1, len(chunks), chunk)
		} else {
			body = fmt.Sprintf("## Implementation Plan (Part %d of %d)\n\n%s%s", i+1, len(chunks), chunk, footer)
		}
		comment, _, err := n.client.Issues.CreateComment(ctx, owner, repo, issue,
			&gogithub.IssueComment{Body: gogithub.Ptr(body)})
		if err != nil {
			return 0, fmt.Errorf("posting plan part %d of %d: %w", i+1, len(chunks), err)
		}
		lastID = comment.GetID()
	}
	return lastID, nil
}

// CheckApproval checks if the plan comment has been approved via a thumbs-up reaction,
// or if a human has left feedback as a reply.
func (n *Notifier) CheckApproval(ctx context.Context, owner, repo string, issue int, commentID int64) (ApprovalResult, error) {
	// Check reactions on the plan comment.
	reactions, _, reactErr := n.client.Reactions.ListIssueCommentReactions(ctx, owner, repo, commentID,
		&gogithub.ListReactionOptions{ListOptions: gogithub.ListOptions{PerPage: 100}})
	if reactErr != nil {
		n.logger.Error("failed to list reactions, falling back to comment check", "error", reactErr)
	} else {
		for _, r := range reactions {
			if r.GetContent() == "+1" {
				// Fetch the plan comment to check the test checkbox and decision states.
				comment, _, err := n.client.Issues.GetComment(ctx, owner, repo, commentID)
				if err != nil {
					return ApprovalResult{Approved: true}, nil
				}
				body := comment.GetBody()
				return ApprovalResult{
					Approved:  true,
					RunTests:  strings.Contains(body, "- [x] Run tests before creating PR"),
					Decisions: extractCheckedDecisions(body),
				}, nil
			}
		}
	}

	// Check for human comments posted after the plan comment.
	comments, _, err := n.client.Issues.ListComments(ctx, owner, repo, issue,
		&gogithub.IssueListCommentsOptions{
			ListOptions: gogithub.ListOptions{PerPage: 100},
		})
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("listing comments: %w", err)
	}

	for _, c := range comments {
		if c.GetID() > commentID && c.GetUser() != nil && c.GetUser().GetType() != "Bot" {
			planComment, _, planErr := n.client.Issues.GetComment(ctx, owner, repo, commentID)
			decisions := ""
			if planErr == nil {
				decisions = extractCheckedDecisions(planComment.GetBody())
			}
			return ApprovalResult{
				Feedback:  c.GetBody(),
				Decisions: decisions,
			}, nil
		}
	}

	return ApprovalResult{}, nil
}

// NotifyImplementationStarted posts a comment indicating implementation has begun.
func (n *Notifier) NotifyImplementationStarted(ctx context.Context, owner, repo string, issue int) error {
	body := "## Implementation Started\n\nThe plan has been approved. Implementation is now in progress..."
	return n.postComment(ctx, owner, repo, issue, body)
}

// NotifyComplete posts the completion message with PR link.
func (n *Notifier) NotifyComplete(ctx context.Context, owner, repo string, issue int, prURL string) error {
	body := fmt.Sprintf("## Task Complete\n\nPull request created: %s", prURL)
	return n.postComment(ctx, owner, repo, issue, body)
}

// NotifyFailed posts a failure message.
func (n *Notifier) NotifyFailed(ctx context.Context, owner, repo string, issue int, reason string) error {
	body := fmt.Sprintf("## Task Failed\n\n%s", reason)
	return n.postComment(ctx, owner, repo, issue, body)
}

// CloseIssue closes a GitHub issue.
func (n *Notifier) CloseIssue(ctx context.Context, owner, repo string, issue int) error {
	state := "closed"
	_, _, err := n.client.Issues.Edit(ctx, owner, repo, issue,
		&gogithub.IssueRequest{State: &state})
	return err
}

// extractCheckedDecisions finds all checked checkbox lines (- [x] ...) in a comment body,
// excluding the "Run tests before creating PR" checkbox which is handled separately.
func extractCheckedDecisions(body string) string {
	var decisions []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x] ") && !strings.Contains(trimmed, "Run tests before creating PR") {
			decisions = append(decisions, trimmed)
		}
	}
	return strings.Join(decisions, "\n")
}

// maxCommentLen is the maximum length for a single GitHub comment body.
// GitHub's hard limit is 65,536 characters; we use 60,000 to leave headroom.
const maxCommentLen = 60000

// splitComment splits a long comment body into chunks that each fit within maxLen.
// It tries to split at natural markdown boundaries: section headers ("\n## "),
// horizontal rules ("\n---\n"), or paragraph breaks ("\n\n"). Falls back to
// splitting at the last newline before maxLen if no better boundary is found.
func splitComment(body string, maxLen int) []string {
	if len(body) <= maxLen {
		return []string{body}
	}

	var chunks []string
	remaining := body

	for len(remaining) > maxLen {
		chunk := remaining[:maxLen]

		// Try split points in order of preference.
		splitIdx := -1
		for _, sep := range []string{"\n## ", "\n---\n", "\n\n"} {
			if idx := strings.LastIndex(chunk, sep); idx > 0 {
				splitIdx = idx
				break
			}
		}
		// Fallback: split at last newline.
		if splitIdx <= 0 {
			if idx := strings.LastIndex(chunk, "\n"); idx > 0 {
				splitIdx = idx
			} else {
				// No newline at all — hard split.
				splitIdx = maxLen
			}
		}

		chunks = append(chunks, strings.TrimRight(remaining[:splitIdx], "\n"))
		remaining = strings.TrimLeft(remaining[splitIdx:], "\n")
	}

	if len(remaining) > 0 {
		chunks = append(chunks, remaining)
	}

	return chunks
}

func (n *Notifier) postComment(ctx context.Context, owner, repo string, issue int, body string) error {
	_, _, err := n.client.Issues.CreateComment(ctx, owner, repo, issue,
		&gogithub.IssueComment{Body: gogithub.Ptr(body)})
	return err
}

func statusEmoji(status string) string {
	switch status {
	case "Running":
		return "🔄"
	case "Succeeded":
		return "✅"
	case "Failed":
		return "❌"
	default:
		return "ℹ️"
	}
}
