package copilotbridge_test

import (
	"context"
	"errors"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
	"github.com/steveyegge/gastown/internal/toolapi"
)

func TestToolsIncludesMayorPatrolTools(t *testing.T) {
	tools, available, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
		Binding:  copilotbridge.BindingInfo{Role: "mayor"},
		TownRoot: "/tmp/town",
		WorkDir:  "/tmp/town/mayor",
		Policy:   config.ToolPolicy{AvailableTools: []string{"bd_show", "bd_ready", "bd_update", "bd_close", "bd_create", "nudge_agent", "send_mail"}},
	})
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 7 || len(available) != 7 {
		t.Fatalf("tools=%d available=%d", len(tools), len(available))
	}
}

func TestTypedSendMailReportsToolErrorOnFailure(t *testing.T) {
	var reported []string
	tools, _, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
		Binding:  copilotbridge.BindingInfo{Role: "mayor"},
		TownRoot: "/tmp/town",
		WorkDir:  "/tmp/town/mayor",
		Policy:   config.ToolPolicy{AvailableTools: []string{"send_mail"}},
		Hooks: toolapi.Callbacks{
			SendMail: func(from, to, subject, body, priority string) error {
				return errors.New("mail backend down")
			},
			ReportToolError: func(message string) {
				reported = append(reported, message)
			},
		},
	})
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want 1 send_mail tool", tools)
	}
	_, err = tools[0].Handler(copilot.ToolInvocation{Arguments: map[string]any{"to": "mayor/", "subject": "hello", "body": "body"}})
	if err == nil {
		t.Fatal("send_mail handler error = nil, want mail backend error")
	}
	if got := err.Error(); got != "mail backend down" {
		t.Fatalf("send_mail handler error = %q, want mail backend down", got)
	}
	if len(reported) != 1 || reported[0] != "mail backend down" {
		t.Fatalf("ReportToolError messages = %#v, want [mail backend down]", reported)
	}
}

func TestTypedNudgeAgentReportsResolutionAndQueueFailures(t *testing.T) {
	t.Run("resolve failure", func(t *testing.T) {
		var reported []string
		tools, _, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
			Binding:  copilotbridge.BindingInfo{Role: "mayor"},
			TownRoot: "/tmp/town",
			WorkDir:  "/tmp/town/mayor",
			Policy:   config.ToolPolicy{AvailableTools: []string{"nudge_agent"}},
			Hooks: toolapi.Callbacks{
				ResolveSessionName: func(target string) (string, error) {
					return "", errors.New("unknown target")
				},
				QueueNudge: func(sessionName, sender, message string) error { return nil },
				ReportToolError: func(message string) {
					reported = append(reported, message)
				},
			},
		})
		if err != nil {
			t.Fatalf("Tools() error = %v", err)
		}
		_, err = tools[0].Handler(copilot.ToolInvocation{Arguments: map[string]any{"target": "ghost", "message": "wake up"}})
		if err == nil || err.Error() != "unknown target" {
			t.Fatalf("nudge_agent resolve error = %v, want unknown target", err)
		}
		if len(reported) != 1 || reported[0] != "unknown target" {
			t.Fatalf("ReportToolError messages = %#v, want [unknown target]", reported)
		}
	})

	t.Run("queue failure", func(t *testing.T) {
		var reported []string
		tools, _, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
			Binding:  copilotbridge.BindingInfo{Role: "mayor"},
			TownRoot: "/tmp/town",
			WorkDir:  "/tmp/town/mayor",
			Policy:   config.ToolPolicy{AvailableTools: []string{"nudge_agent"}},
			Hooks: toolapi.Callbacks{
				ResolveSessionName: func(target string) (string, error) {
					return "gt-crew-max", nil
				},
				QueueNudge: func(sessionName, sender, message string) error {
					return errors.New("queue write failed")
				},
				ReportToolError: func(message string) {
					reported = append(reported, message)
				},
			},
		})
		if err != nil {
			t.Fatalf("Tools() error = %v", err)
		}
		_, err = tools[0].Handler(copilot.ToolInvocation{Arguments: map[string]any{"target": "gastown/crew/max", "message": "wake up"}})
		if err == nil || err.Error() != "queue write failed" {
			t.Fatalf("nudge_agent queue error = %v, want queue write failed", err)
		}
		if len(reported) != 1 || reported[0] != "queue write failed" {
			t.Fatalf("ReportToolError messages = %#v, want [queue write failed]", reported)
		}
	})
}

func TestTypedSendMailSuccessDoesNotReportToolError(t *testing.T) {
	var reported []string
	tools, _, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
		Binding:  copilotbridge.BindingInfo{Role: "mayor"},
		TownRoot: "/tmp/town",
		WorkDir:  "/tmp/town/mayor",
		Policy:   config.ToolPolicy{AvailableTools: []string{"send_mail"}},
		Hooks: toolapi.Callbacks{
			SendMail: func(from, to, subject, body, priority string) error { return nil },
			ReportToolError: func(message string) {
				reported = append(reported, message)
			},
		},
	})
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	result, err := tools[0].Handler(copilot.ToolInvocation{Arguments: map[string]any{"to": "mayor/", "subject": "hello", "body": "body"}})
	if err != nil {
		t.Fatalf("send_mail handler error = %v", err)
	}
	if result.TextResultForLLM != "sent" {
		t.Fatalf("result = %#v, want sent", result)
	}
	if len(reported) != 0 {
		t.Fatalf("ReportToolError messages = %#v, want none", reported)
	}
}
