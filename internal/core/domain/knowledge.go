package domain

import "time"

// Identifiers of the project knowledge objects (spec sections 13 and 14).
type (
	// DecisionID identifies a Decision.
	DecisionID string
	// TaskID identifies a Task.
	TaskID string
)

// NewDecisionID returns a fresh DecisionID.
func NewDecisionID() DecisionID { return DecisionID(NewUUID()) }

// NewTaskID returns a fresh TaskID.
func NewTaskID() TaskID { return TaskID(NewUUID()) }

// DecisionImportance decides whether a Decision is injected into every new Run
// context or merely searchable.
type DecisionImportance string

const (
	// DecisionImportant is carried into the context of every new Run.
	DecisionImportant DecisionImportance = "IMPORTANT"
	// DecisionNormal is the default: recorded and searchable, not injected.
	DecisionNormal DecisionImportance = "NORMAL"
)

func (i DecisionImportance) String() string { return string(i) }

// Valid reports whether i is a known DecisionImportance.
func (i DecisionImportance) Valid() bool {
	switch i {
	case DecisionImportant, DecisionNormal:
		return true
	default:
		return false
	}
}

// DecisionStatus is the lifecycle of a Decision.
type DecisionStatus string

const (
	DecisionActive DecisionStatus = "ACTIVE"
	// DecisionSuperseded remains historical but is no longer current, and is
	// never injected into a Run context.
	DecisionSuperseded DecisionStatus = "SUPERSEDED"
)

func (s DecisionStatus) String() string { return string(s) }

// Valid reports whether s is a known DecisionStatus.
func (s DecisionStatus) Valid() bool {
	switch s {
	case DecisionActive, DecisionSuperseded:
		return true
	default:
		return false
	}
}

// CanTransition reports whether a Decision may move from s to to. Superseding is
// one-way: a ruling that was replaced is history.
func (s DecisionStatus) CanTransition(to DecisionStatus) bool {
	return s == DecisionActive && to == DecisionSuperseded
}

// Transition validates a Decision status change.
func (s DecisionStatus) Transition(to DecisionStatus) (DecisionStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("decision status", s, to)
	}
	return to, nil
}

// Decision is a durable project ruling (spec section 13).
type Decision struct {
	ID         DecisionID         `json:"id"`
	ProjectID  ProjectID          `json:"projectId"`
	Title      string             `json:"title"`
	Content    string             `json:"content,omitempty"`
	Importance DecisionImportance `json:"importance"`
	Status     DecisionStatus     `json:"status"`
	// Supersedes is the Decision this one replaced, if any.
	Supersedes *DecisionID `json:"supersedes,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
}

// TaskStatus is the lifecycle of a Task (spec section 14).
//
// There is deliberately no BLOCKED status: blocked is derived from incomplete
// dependencies, so it can never drift from the truth.
type TaskStatus string

const (
	TaskTodo       TaskStatus = "TODO"
	TaskInProgress TaskStatus = "IN_PROGRESS"
	TaskDone       TaskStatus = "DONE"
)

func (s TaskStatus) String() string { return string(s) }

// Valid reports whether s is a known TaskStatus.
func (s TaskStatus) Valid() bool {
	_, ok := taskTransitions[s]
	return ok
}

// Done reports whether a Task is finished, which is what unblocks the Tasks
// depending on it.
func (s TaskStatus) Done() bool { return s == TaskDone }

// taskTransitions allows a Task to move freely between open states and to be
// reopened: work is discovered to be incomplete often enough that forcing a new
// Task would lose the history of the original one.
var taskTransitions = map[TaskStatus]map[TaskStatus]bool{
	TaskTodo:       {TaskInProgress: true, TaskDone: true},
	TaskInProgress: {TaskTodo: true, TaskDone: true},
	TaskDone:       {TaskTodo: true, TaskInProgress: true},
}

// CanTransition reports whether a Task may move from s to to.
func (s TaskStatus) CanTransition(to TaskStatus) bool {
	allowed, ok := taskTransitions[s]
	if !ok {
		return false
	}
	return allowed[to]
}

// Transition validates a Task status change.
func (s TaskStatus) Transition(to TaskStatus) (TaskStatus, error) {
	if !s.CanTransition(to) {
		return s, transitionError("task status", s, to)
	}
	return to, nil
}

// Task is a unit of project work (spec section 14).
type Task struct {
	ID          TaskID     `json:"id"`
	ProjectID   ProjectID  `json:"projectId"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Status      TaskStatus `json:"status"`
	// DependsOn lists the Tasks that must be DONE before this one can start.
	DependsOn   []TaskID   `json:"dependsOn,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// Blocked reports whether a Task is waiting on a dependency, given the statuses
// of the Tasks it depends on. It is derived rather than stored, which is why
// there is no BLOCKED status to keep in sync.
func (t Task) Blocked(statuses map[TaskID]TaskStatus) bool {
	for _, id := range t.DependsOn {
		if !statuses[id].Done() {
			return true
		}
	}
	return false
}
