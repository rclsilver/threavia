package tools

func reviewSpecs() []Spec {
	return []Spec{
		{Name: NameReviewRepositoryRegister, Description: "Declare a repository or worktree BEFORE changing files in it, so the human review includes its initial baseline. The current job's repository is captured automatically; declare every extra repository, including newly created worktrees. A late declaration cannot reconstruct earlier edits.", InputSchema: object(map[string]any{"directory": str("Absolute repository or worktree path on this backend."), "name": str("Short name shown in the review tree.")}, "directory")},
		{Name: NameReviewRead, Description: "Read published human code-review discussions and revision identifiers in this session. Human drafts are private until sent.", InputSchema: object(map[string]any{})},
		{Name: NameReviewRequest, Description: "Capture the current changes and signal that they are ready for human review. When the user explicitly requires review, set required true: further actions wait at the next execution checkpoint until the human approves or requests a correction turn. Never approve on their behalf.", InputSchema: object(map[string]any{"required": map[string]any{"type": "boolean", "description": "Set when human validation is mandatory before continuing."}})},
		{Name: NameReviewReply, Description: "Reply to a published human review thread, explaining a correction or answering a question. The human resolves discussions and approves the result.", InputSchema: object(map[string]any{"threadId": str("Published thread identifier."), "body": str("Outcome of the correction or answer.")}, "threadId", "body")},
	}
}
