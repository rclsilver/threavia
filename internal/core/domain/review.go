package domain

import "time"

// Review is a session's durable human review. Code remains backend-owned.
type Review struct {
	SessionID       SessionID          `json:"sessionId"`
	Version         int                `json:"version"`
	Required        bool               `json:"required"`
	Status          string             `json:"status"`
	CorrectionJobID JobID              `json:"correctionJobId,omitempty"`
	Repositories    []ReviewRepository `json:"repositories"`
	Revisions       []ReviewRevision   `json:"revisions"`
	Threads         []ReviewThread     `json:"threads"`
	Seen            map[string]string  `json:"seen"`
	UpdatedAt       time.Time          `json:"updatedAt"`
}

type ReviewRepository struct {
	ID                string            `json:"id"`
	BackendInstanceID BackendInstanceID `json:"backendInstanceId"`
	Name              string            `json:"name"`
	Directory         string            `json:"directory"`
	GitDirectory      string            `json:"gitDirectory"`
	InitialTree       string            `json:"initialTree"`
	ApprovedTree      string            `json:"approvedTree"`
	BaselineSource    string            `json:"baselineSource"`
}

type ReviewRevision struct {
	ID           string                     `json:"id"`
	CreatedAt    time.Time                  `json:"createdAt"`
	Repositories []ReviewRevisionRepository `json:"repositories"`
	Approved     bool                       `json:"approved"`
}

type ReviewRevisionRepository struct {
	RepositoryID string       `json:"repositoryId"`
	BaseTree     string       `json:"baseTree"`
	HeadTree     string       `json:"headTree"`
	Files        []ReviewFile `json:"files"`
	Error        string       `json:"error,omitempty"`
}

type ReviewFile struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath,omitempty"`
	State   string `json:"state"`
}

type ReviewThread struct {
	ID           string          `json:"id"`
	RevisionID   string          `json:"revisionId"`
	RepositoryID string          `json:"repositoryId,omitempty"`
	Path         string          `json:"path,omitempty"`
	Side         string          `json:"side,omitempty"`
	StartLine    int             `json:"startLine,omitempty"`
	EndLine      int             `json:"endLine,omitempty"`
	Excerpt      string          `json:"excerpt,omitempty"`
	Resolved     bool            `json:"resolved"`
	Messages     []ReviewMessage `json:"messages"`
}

type ReviewMessage struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	Draft     bool      `json:"draft"`
	BatchID   string    `json:"batchId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type ReviewFileContent struct {
	Path      string `json:"path"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Diff      string `json:"diff"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}
