package commands

import (
	"context"
	"fmt"
	"log"

	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

const channelHistoryLimit = 20

type DebugHandler struct {
	slackClient     SlackClient
	ghClient        *github.Client
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
	memory          *ConversationMemory
	prompts         PromptProvider
}

func (h *DebugHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	channelContext, err := h.contextProvider.GetFreshChannelContext(channelID)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to fetch channel context: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to read channel history: %v", err), true)
		return
	}

	if channelContext == "(no recent messages)" || channelContext == "(no recent messages with content)" {
		_ = ovadslack.RespondToURL(responseURL, "No messages found in this channel to analyze.", true)
		return
	}

	workflowLogs := h.fetchWorkflowLogs(ctx, channelContext+"\n"+text, userID, channelID)

	systemPrompt := h.prompts.MustGet("security") + "\n\n" + h.prompts.MustGet("debug")

	userPrompt := fmt.Sprintf("Here are the recent messages from the channel:\n\n%s\n\nUser request: %s", channelContext, text)
	if workflowLogs != "" {
		userPrompt += fmt.Sprintf("\n\nI also fetched the GitHub Actions workflow run details and logs for URLs found in the messages:\n\n%s", workflowLogs)
	}

	response, err := h.modelsClient.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		log.Printf("[user=%s channel=%s] LLM completion failed: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to analyze messages: %v", err), true)
		return
	}

	log.Printf("[user=%s channel=%s] debug analysis completed successfully", userID, channelID)
	h.memory.SetAssistantResponse(channelID, userID, response)
	if err := ovadslack.RespondToURL(responseURL, response, false); err != nil {
		log.Printf("[user=%s channel=%s] failed to post debug response: %v", userID, channelID, err)
	}
}

func (h *DebugHandler) fetchWorkflowLogs(ctx context.Context, channelContext, userID, channelID string) string {
	urls := github.ExtractWorkflowRunURLs(channelContext)
	if len(urls) == 0 {
		return ""
	}

	seen := make(map[string]bool)
	var result string
	for _, u := range urls {
		if seen[u] {
			continue
		}
		seen[u] = true

		owner, repo, runID, err := github.ParseWorkflowRunURL(u)
		if err != nil {
			continue
		}

		log.Printf("[user=%s channel=%s] fetching workflow run %s/%s/%d", userID, channelID, owner, repo, runID)
		summary, err := h.ghClient.GetWorkflowRunSummary(ctx, owner, repo, runID)
		if err != nil {
			log.Printf("[user=%s channel=%s] failed to fetch workflow run summary: %v", userID, channelID, err)
			continue
		}

		result += github.FormatWorkflowRunSummary(summary)
	}
	return result
}
