package domain

// UsageRow is what the Jobs of one Session consumed with one backend, model
// and effort over a period. Figures are those backends reported; Jobs minus
// ReportedJobs is how many consumed an unknown amount.
type UsageRow struct {
	SessionID         SessionID         `json:"sessionId"`
	SessionTitle      string            `json:"sessionTitle"`
	ProjectID         ProjectID         `json:"projectId"`
	ProjectName       string            `json:"projectName"`
	BackendInstanceID BackendInstanceID `json:"backendInstanceId"`
	Model             string            `json:"model"`
	Effort            string            `json:"effort"`
	Jobs              int64             `json:"jobs"`
	ReportedJobs      int64             `json:"reportedJobs"`
	InputTokens       int64             `json:"inputTokens"`
	OutputTokens      int64             `json:"outputTokens"`
	CacheReadTokens   int64             `json:"cacheReadTokens"`
	CacheWriteTokens  int64             `json:"cacheWriteTokens"`
	CostUSD           float64           `json:"costUsd"`
}
