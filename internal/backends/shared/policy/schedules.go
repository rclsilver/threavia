package policy

// Claude Code 2.1.295 exposes these names. Keep the gate and native settings
// aligned: native timers belong to the provider process, not the session.
var nativeScheduleTools = []string{"CronCreate", "CronDelete", "CronList", "ScheduleWakeup"}
