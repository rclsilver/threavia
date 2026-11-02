package tools

func scheduleSpecs() []Spec {
	fields := func() map[string]any {
		return map[string]any{
			"cron":     str("Five-field cron expression: minute hour day-of-month month day-of-week."),
			"timezone": str("IANA timezone, such as Europe/Paris (the default on creation)."),
			"message":  str("Message posted in this session at each due time."),
			"enabled":  map[string]any{"type": "boolean", "description": "False pauses the schedule; true resumes it. Defaults to true on creation."},
			"quiet":    map[string]any{"type": "boolean", "description": "True lets each run end silently with schedule_dismiss when there is nothing to report: no notification, and the run is folded away. Defaults to false on creation."},
		}
	}
	update := fields()
	update["scheduleId"] = str("Identifier of a schedule in this session, from schedule_list.")
	return []Spec{
		{Name: NameScheduleList, Description: "List this session's persistent Threavia schedules, including paused schedules and upcoming runs.", InputSchema: object(map[string]any{})},
		{Name: NameScheduleCreate, Description: "Schedule a recurring message in this session using Threavia. It survives the current job. Runs are skipped when the session is busy, the backend is offline or the due time was missed; they are never accumulated or sent late.", InputSchema: object(fields(), "cron", "message")},
		{Name: NameScheduleUpdate, Description: "Change, pause or resume a Threavia schedule in this session. Omitted fields are preserved; the next run is recalculated from now.", InputSchema: object(update, "scheduleId")},
		{Name: NameScheduleDelete, Description: "Delete a Threavia schedule in this session. Previously posted messages stay in the conversation.", InputSchema: object(map[string]any{"scheduleId": str("Identifier from schedule_list.")}, "scheduleId")},
	}
}

// DismissSpec is offered only to a Job a quiet schedule started: anywhere else
// the person asked for an answer, and ending without one is not an option.
func DismissSpec() Spec {
	return Spec{Name: NameScheduleDismiss, Description: "This message was posted by a quiet Threavia schedule. When the check finds nothing the person asked to hear about, call this, then end with a one-line summary: the run ends without notifying the person and is folded away in the conversation. Do not call it when there is something to report; answer normally instead.", InputSchema: object(map[string]any{})}
}
