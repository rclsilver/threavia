package tools

func monitorSpecs() []Spec {
	return []Spec{
		{Name: "monitor_list", Description: "List this session's persistent Threavia monitors, including recent results and whether their activity is confirmed. These survive individual Jobs.", InputSchema: object(map[string]any{})},
		{Name: "monitor_stop", Description: "Stop one persistent Threavia monitor in this session. Offline stops are retained and delivered when its backend reconnects.", InputSchema: object(map[string]any{"monitorId": str("Identifier from monitor_list.")}, "monitorId")},
	}
}
