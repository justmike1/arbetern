package commands

import (
	"context"
	"fmt"
	"log"

	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

const channelHistoryLimit = 20

type DebugHandler struct {
	slackClient     SlackClient
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
}

func (h *DebugHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	channelContext, err := h.contextProvider.GetChannelContext(channelID)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to fetch channel context: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to read channel history: %v", err), true)
		return
	}

	if channelContext == "(no recent messages)" {
		_ = ovadslack.RespondToURL(responseURL, "No messages found in this channel to analyze.", true)
		return
	}

	systemPrompt := prompts.MustGet("debug")

	userPrompt := fmt.Sprintf("Here are the recent messages from the channel:\n\n%s\n\nUser request: %s", channelContext, text)

	response, err := h.modelsClient.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		log.Printf("[user=%s channel=%s] LLM completion failed: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to analyze messages: %v", err), true)
		return
	}

	log.Printf("[user=%s channel=%s] debug analysis completed successfully", userID, channelID)
	if err := ovadslack.RespondToURL(responseURL, response, false); err != nil {
		log.Printf("[user=%s channel=%s] failed to post debug response: %v", userID, channelID, err)
	}
}
