package runner

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
)

// SystemPrompt tells the agent about the Threavia-specific tools and about the
// project it is working on. Every provider receives the same text, through
// whatever mechanism it has for instructions that are not the user's.
func SystemPrompt(params StartParams, available []string, scratch string) string {
	var b strings.Builder
	b.WriteString("You are running inside Threavia, a control plane for coding agents. ")
	b.WriteString("There is no interactive terminal: the user may be on another device entirely, ")
	b.WriteString("and may take a long time to answer.\n\n")
	b.WriteString("Whenever you need information, a choice or a decision from the user, ")
	b.WriteString("call the mcp__" + mcp.ServerName + "__" + mcp.ToolAskUser + " tool and wait for the answer. ")
	b.WriteString("Never guess, and never stop and ask in plain text: a plain-text question reaches nobody.\n")

	// Said here because the agent cannot see it, and because the habit it
	// otherwise forms is expensive: a command that assigns PATH needs the
	// user's approval whatever else it does, so one prefix turns every reading
	// command into a question for someone who may be asleep.
	//
	// Named rather than described. "Your PATH carries the tools for this work"
	// is a claim the agent has no way to check and every reason to doubt, and
	// doubt is what produces the prefix. The list is read from PATH at start,
	// so adding a package to the service is the whole of adding it here.
	b.WriteString("\nYou can run these by name, they are already on your PATH:\n")
	if len(available) > 0 {
		b.WriteString(strings.Join(available, " ") + "\n")
	}
	b.WriteString("Never prefix a command with an assignment to PATH or to another special shell ")
	b.WriteString("variable: it costs an approval the command would not otherwise need, ")
	b.WriteString("however harmless the rest of it is. ")
	b.WriteString("If something you need is genuinely missing, say so rather than working around it.\n")

	// Named because the alternative is /tmp, and a file in /tmp has to be
	// approved to be read back — the agent asking permission for what it wrote
	// a second earlier. This one is readable without asking, and it is not
	// shared with everything else on the machine.
	if scratch != "" {
		b.WriteString("\nWrite any intermediate file in " + scratch + ", never in /tmp. ")
		b.WriteString("It belongs to this session, you can read back from it without asking, ")
		b.WriteString("and it outlives the message you are answering.\n")
	}

	if len(params.CoreTools) > 0 {
		// The tool descriptions say when to use each one; this says why they
		// exist at all, which is the part an agent cannot infer from a schema.
		b.WriteString("\nThis project has a memory that outlives this conversation: ")
		b.WriteString("decisions, tasks and the history of what was already done, possibly ")
		b.WriteString("in another session or on another machine. The mcp__" + mcp.ServerName)
		b.WriteString("__ tools read and write it. Search it before assuming work is new, ")
		b.WriteString("and record what a later session would otherwise have to rediscover.\n")

		// The person reads the conversation, not this machine: a page or a
		// chart left in a directory is one they never see. The tool that
		// carries a file is found by what it declares, not by its name.
		for _, tool := range params.CoreTools {
			if tool.FileInput == "" {
				continue
			}
			b.WriteString("\nThe user cannot open files on this machine. When you make something for ")
			b.WriteString("them to look at — a page, a chart, an image, a report — write it to a file, ")
			b.WriteString("then publish it with mcp__" + mcp.ServerName + "__" + tool.Name + ": it appears ")
			b.WriteString("in the conversation, where they can open it. This is the only way to show them ")
			b.WriteString("a file. Do not give them a local path to open, do not start a web server or ")
			b.WriteString("open a browser on this machine, do not paste the file into your answer, and ")
			b.WriteString("do not upload it anywhere else (a gist, a paste service, a bucket): none of ")
			b.WriteString("that reaches them, or it reaches others.\n")
			break
		}
	}

	if params.ProjectName != "" {
		b.WriteString("\nProject: " + params.ProjectName)
		if params.ProjectDescription != "" {
			b.WriteString(" — " + params.ProjectDescription)
		}
		b.WriteString("\n")
	}
	// Instructions reach the provider through its system prompt rather than
	// through a file: Threavia never writes CLAUDE.md or AGENTS.md into someone
	// else's repository, and a Run must not leave provider configuration behind
	// (spec section 18).
	if instructions := EffectiveInstructions(params); instructions != "" {
		b.WriteString("\nProject instructions, which take precedence over your defaults:\n")
		b.WriteString(instructions)
		b.WriteString("\n")
	}

	if params.SkillDirectory != "" {
		b.WriteString("\nThis project has Skills available to you. Use them when they apply ")
		b.WriteString("rather than improvising the same work from scratch.\n")
	}

	return b.String()
}

// EffectiveInstructions is the provider-independent project rules followed by
// the private rules of this machine, which is the split of specification
// section 18. The local ones come last so a machine can qualify a project rule.
func EffectiveInstructions(params StartParams) string {
	parts := make([]string, 0, 2)
	if project := strings.TrimSpace(params.ProjectInstructions); project != "" {
		parts = append(parts, project)
	}
	if local := strings.TrimSpace(params.LocalInstructions); local != "" {
		parts = append(parts, local)
	}
	return strings.Join(parts, "\n\n")
}

// ExecutablesOnPath lists, once, what a Job can run by name.
//
// Read rather than declared. The agent cannot see its own PATH before running
// something, and the cost of it guessing wrong is not a failed command — it is
// a command prefixed with `export PATH=…:$PATH`, which no permission rule can
// approve and which therefore wakes a person up for a search. A list written by
// hand would answer that until the day someone adds a package, and then answer
// it wrongly, which is worse than not answering: it tells the agent the tool is
// absent when it is there.
func ExecutablesOnPath() []string {
	seen := make(map[string]struct{})
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			// Wrapped internals and dotfiles are noise in a list meant to be read.
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			seen[entry.Name()] = struct{}{}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	// A PATH nobody curated can hold thousands. Past this the list stops being
	// information and starts being noise in every Job's prompt.
	const most = 400
	if len(names) > most {
		names = names[:most]
	}
	return names
}

// scratchKept is how long an abandoned Run's files stay. Long enough that a
// Session picked up the next morning still finds what it left, short enough
// that the state directory does not grow for ever.
const scratchKept = 7 * 24 * time.Hour

// SweepScratch removes what previous Runs left behind and nobody came back for.
func SweepScratch(root string, logger *slog.Logger) {
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < scratchKept {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			logger.Warn("cannot remove an abandoned scratch directory",
				slog.String("path", filepath.Join(root, entry.Name())),
				slog.String("error", err.Error()))
		}
	}
}
