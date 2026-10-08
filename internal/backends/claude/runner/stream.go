package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// streamLine is one line of Claude Code --output-format stream-json output.
//
// Only the fields Threavia normalises are decoded; everything else the provider
// emits is deliberately ignored rather than leaked into the Core timeline.
type streamLine struct {
	Type      string           `json:"type"`
	Subtype   string           `json:"subtype"`
	SessionID string           `json:"session_id"`
	Message   *providerMessage `json:"message"`
	IsError   bool             `json:"is_error"`
	Result    string           `json:"result"`

	// What the turn consumed, reported on the result line.
	Usage    *providerUsage `json:"usage"`
	CostUSD  float64        `json:"total_cost_usd"`
	Duration int64          `json:"duration_ms"`
}

// providerUsage is the subset of the provider accounting Threavia keeps.
//
// The provider reports far more — per-model breakdowns, service tiers, web
// search counts. Core normalises to what every provider can be expected to
// have, so a timeline reads the same whichever one produced it.
type providerUsage struct {
	InputTokens         uint64 `json:"input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	CacheReadTokens     uint64 `json:"cache_read_input_tokens"`
	CacheCreationTokens uint64 `json:"cache_creation_input_tokens"`
}

type providerMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// outcome is what the provider output said about how the Job ended.
type outcome struct {
	completed bool
	failed    bool
	summary   string
	// stoppedBy is set when a policy limit ended the Run rather than the agent.
	stoppedBy string
	// usage is nil when the provider reported none, which a terminal event
	// carries through: no accounting is not the same as a free Job.
	usage *backendv1.Usage
}

// consume reads the provider output stream and emits normalised events.
func (c *Claude) consume(ctx context.Context, stdout io.Reader, params StartParams, sink Sink) outcome {
	// Remembering which tool each call id belongs to lets a tool result be
	// reported with the name of the tool it answers.
	toolNames := make(map[string]string)
	var (
		result  outcome
		actions int
	)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	echoes := 0

	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		// A message the provider read, echoed back. Its content is a plain
		// string rather than blocks, and it is not news: Core already put it
		// in the timeline when it was sent.
		var head struct {
			Type     string `json:"type"`
			IsReplay bool   `json:"isReplay"`
		}
		if err := json.Unmarshal([]byte(raw), &head); err == nil && head.Type == "user" && head.IsReplay {
			c.consumedInput(params.JobID, echoes == 0)
			echoes++
			continue
		}

		var line streamLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			c.logger.Debug("ignoring unparsable provider output", slog.String("line", truncate(raw, 200)))
			continue
		}

		switch line.Type {
		case "system":
			// The init frame confirms the session the provider actually used.
			if line.Subtype == "init" && line.SessionID != "" {
				sink.Progress(ctx, params.RunID, params.JobID, "agent.working", "session ready")
			}

		case "assistant":
			if stopped := c.emitAssistant(ctx, params, line, toolNames, sink, &actions); stopped != "" {
				result.stoppedBy = stopped
				return result
			}

		case "user":
			c.emitToolResults(ctx, params, line, toolNames, sink)

		case "result":
			// A Job can span several turns of one process, when a message
			// reached it while it ran. The last turn says how it ended; the
			// accounting covers all of them.
			result.completed = true
			result.failed = line.IsError
			result.summary = line.Result
			if result.summary == "" && line.IsError {
				result.summary = "claude code reported an error (" + line.Subtype + ")"
			}
			result.usage = addUsage(result.usage, normaliseUsage(line))
			c.turnEnded(params.JobID)

		default:
			// Rate limit notices and other provider bookkeeping are liveness at
			// best and are never persisted.
			c.logger.Debug("ignoring provider frame", slog.String("type", line.Type))
		}
	}

	if err := scanner.Err(); err != nil {
		c.logger.Warn("provider output ended early", slog.String("error", err.Error()))
	}
	return result
}

// emitAssistant turns an assistant turn into agent messages and tool calls.
func (c *Claude) emitAssistant(ctx context.Context, params StartParams, line streamLine, toolNames map[string]string, sink Sink, actions *int) string {
	if line.Message == nil {
		return ""
	}
	for _, block := range line.Message.Content {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				if err := sink.AgentMessage(ctx, params.RunID, params.JobID, text); err != nil {
					c.logger.Error("cannot report an agent message", slog.String("error", err.Error()))
				}
			}

		case "tool_use":
			toolNames[block.ID] = block.Name
			*actions++
			if limit := params.Policy.MaxActions; limit > 0 && *actions > limit {
				// Counted here because this is where actions are visible. The
				// caller stops the process; reporting it as a policy stop is
				// what keeps it from looking like a crash.
				return fmt.Sprintf("stopped by the execution policy after %d actions", limit)
			}
			if err := sink.ToolStarted(ctx, params.RunID, params.JobID,
				block.ID, block.Name, decodeObject(block.Input)); err != nil {
				c.logger.Error("cannot report a tool call", slog.String("error", err.Error()))
			}
		}
	}
	return ""
}

// emitToolResults turns the synthetic user turn carrying tool results into tool
// completion events.
func (c *Claude) emitToolResults(ctx context.Context, params StartParams, line streamLine, toolNames map[string]string, sink Sink) {
	if line.Message == nil {
		return
	}
	for _, block := range line.Message.Content {
		if block.Type != "tool_result" {
			continue
		}

		name := toolNames[block.ToolUseID]
		text := flattenContent(block.Content)
		if block.IsError {
			if err := sink.ToolFailed(ctx, params.RunID, params.JobID,
				block.ToolUseID, name, truncate(text, 4000)); err != nil {
				c.logger.Error("cannot report a tool failure", slog.String("error", err.Error()))
			}
			continue
		}
		// Tool output can be very large; the timeline keeps a bounded excerpt and
		// the full content stays on the backend.
		if err := sink.ToolCompleted(ctx, params.RunID, params.JobID, block.ToolUseID, name,
			map[string]any{"output": truncate(text, 4000)}); err != nil {
			c.logger.Error("cannot report a tool result", slog.String("error", err.Error()))
		}
	}
}

// decodeObject renders a tool input as a plain map, or an empty one.
func decodeObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return map[string]any{}
	}
	return decoded
}

// flattenContent renders a tool result, which the provider sends either as a
// string or as a list of content blocks.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}

// truncate bounds a string on a rune boundary.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// addUsage folds the accounting of one more turn into a Job's. Tokens are
// reported per turn and add up; the cost is the process total so far, so the
// latest one is the whole of it.
func addUsage(total, turn *backendv1.Usage) *backendv1.Usage {
	if total == nil {
		return turn
	}
	if turn == nil {
		return total
	}
	return &backendv1.Usage{
		InputTokens:      total.InputTokens + turn.InputTokens,
		OutputTokens:     total.OutputTokens + turn.OutputTokens,
		CacheReadTokens:  total.CacheReadTokens + turn.CacheReadTokens,
		CacheWriteTokens: total.CacheWriteTokens + turn.CacheWriteTokens,
		CostUsd:          turn.CostUsd,
	}
}

// normaliseUsage turns what the provider reported into the protocol shape.
//
// It returns nil when the provider said nothing: a Job whose accounting is
// unknown must not look like a Job that cost nothing.
func normaliseUsage(line streamLine) *backendv1.Usage {
	if line.Usage == nil && line.CostUSD == 0 {
		return nil
	}

	usage := &backendv1.Usage{CostUsd: line.CostUSD}
	if line.Usage != nil {
		usage.InputTokens = line.Usage.InputTokens
		usage.OutputTokens = line.Usage.OutputTokens
		usage.CacheReadTokens = line.Usage.CacheReadTokens
		usage.CacheWriteTokens = line.Usage.CacheCreationTokens
	}
	return usage
}
