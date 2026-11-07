package mcp

// tool is one entry of the tools/list answer.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func object(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func toolList() []tool {
	return []tool{
		{
			Name: ToolPublishEvent,
			Description: "Publish one step of an agent run into AgentPulse. Rules decide how important it is and whether anyone is interrupted; " +
				"the severity you give is only data for those rules and cannot make an event critical. Chain steps with traceId and parentEventId.",
			InputSchema: object([]string{"eventType"}, map[string]any{
				"eventType":     str("What happened, such as agent.plan, tool.call.failed, agent.retry or agent.complete."),
				"id":            str("Optional idempotency key. Sending the same id twice stores one event."),
				"traceId":       str("Identifies the run. Every step of one run shares it."),
				"parentEventId": str("The step that led to this one. Needs traceId."),
				"agentName":     str("Which agent is reporting."),
				"source":        str("Where the event comes from. Defaults to agent."),
				"repository":    str("Optional repository or project the run is about."),
				"severity":      map[string]any{"type": "string", "enum": []string{"info", "low", "medium", "high", "critical"}, "description": "How serious the agent thinks this is. Data for rules only."},
				"metadata": map[string]any{
					"type":        "object",
					"description": "Telemetry: step_kind (plan, tool_call, retry, reflect, human_approval, complete), tool_name, attempt, duration_ms, tokens_in, tokens_out, cost_usd, confidence (0 to 1).",
				},
				"payload": map[string]any{"type": "object", "description": "Any other details to keep with the event."},
			}),
		},
		{
			Name:        ToolGetRecentEvents,
			Description: "Read the most recent events, newest first, with the attention class and decision for each. Give traceId to read one agent run.",
			InputSchema: object(nil, map[string]any{
				"traceId": str("Only events of this run."),
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": maxRecentLimit, "description": "How many to return. Defaults to 20."},
			}),
		},
		{
			Name: ToolNotifyUser,
			Description: "Send a notification through the platform. It is validated like any other: only allowed hosts and recipient domains are accepted. " +
				"Use publish_event instead when you want the rules to decide whether anyone should be interrupted.",
			InputSchema: object([]string{"title", "message"}, map[string]any{
				"title":            str("A short headline."),
				"message":          str("The message body."),
				"agent":            str("Which agent is sending it."),
				"emailRecipients":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Email addresses."},
				"slackWebhookUrls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Slack incoming webhook URLs on an allowed host."},
				"teamsWebhookUrls": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Teams webhook URLs on an allowed host."},
			}),
		},
		{
			Name:        ToolExplainDecision,
			Description: "Say in plain words why an event was handled the way it was: which rule applied, whether a person's choice or a budget held it, and whether a model took part.",
			InputSchema: object([]string{"eventId"}, map[string]any{"eventId": str("The event to explain.")}),
		},
	}
}
