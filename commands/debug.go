package commands

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

const channelHistoryLimit = 20

type DebugHandler struct {
	slackClient  SlackClient
	modelsClient *github.ModelsClient
}

func (h *DebugHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	messages, err := h.slackClient.FetchChannelHistory(channelID, channelHistoryLimit)
	if err != nil {
		log.Printf("failed to fetch channel history: %v", err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to read channel history: %v", err), true)
		return
	}

	if len(messages) == 0 {
		_ = ovadslack.RespondToURL(responseURL, "No messages found in this channel to analyze.", true)
		return
	}

	var sb strings.Builder
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		fmt.Fprintf(&sb, "[%s]: %s\n", msg.User, msg.Text)
	}

	systemPrompt := `You are a DevOps and SRE expert. You are given recent messages from a Slack alerting channel.
Analyze the messages, identify any errors, alerts, or issues, and provide:
1. A summary of what happened
2. The likely root cause
3. Suggested next steps or remediation

Be concise and actionable.`

	userPrompt := fmt.Sprintf("Here are the recent messages from the channel:\n\n%s\n\nUser request: %s", sb.String(), text)

	response, err := h.modelsClient.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		log.Printf("LLM completion failed: %v", err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to analyze messages: %v", err), true)
		return
	}

	if err := ovadslack.RespondToURL(responseURL, response, false); err != nil {
		log.Printf("failed to post debug response: %v", err)
	}
}
