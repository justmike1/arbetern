package commands

import (
	"context"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
)

type Router struct {
	slackClient  SlackClient
	ghClient     *github.Client
	modelsClient *github.ModelsClient
}

func NewRouter(slackClient SlackClient, ghClient *github.Client, modelsClient *github.ModelsClient) *Router {
	return &Router{
		slackClient:  slackClient,
		ghClient:     ghClient,
		modelsClient: modelsClient,
	}
}

func (r *Router) Handle(channelID, userID, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		r.replyError(channelID, userID, "Please provide a command. Example: `/ovad please debug the latest message in this channel`")
		return
	}

	lower := strings.ToLower(text)

	switch {
	case isDebugIntent(lower):
		handler := &DebugHandler{
			slackClient:  r.slackClient,
			modelsClient: r.modelsClient,
		}
		handler.Execute(channelID, userID, text)

	case isFileModIntent(lower):
		handler := &FileModHandler{
			slackClient:  r.slackClient,
			ghClient:     r.ghClient,
			modelsClient: r.modelsClient,
		}
		handler.Execute(channelID, userID, text)

	default:
		r.handleAmbiguous(channelID, userID, text)
	}
}

func isDebugIntent(text string) bool {
	debugKeywords := []string{"debug", "analyze", "investigate", "diagnose", "what happened", "explain the error", "look at the latest"}
	for _, kw := range debugKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func isFileModIntent(text string) bool {
	modKeywords := []string{"add env", "modify", "update file", "change file", "edit file", "add variable", "in repository", "in repo"}
	for _, kw := range modKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func (r *Router) handleAmbiguous(channelID, userID, text string) {
	ctx := contextBackground()

	systemPrompt := `You are a command classifier. Given a user message, respond with exactly one word:
- "debug" if the user wants to analyze, debug, or investigate messages/alerts
- "filemod" if the user wants to modify, add, change, or update a file in a repository
- "unknown" if you cannot determine the intent`

	result, err := r.modelsClient.Complete(ctx, systemPrompt, text)
	if err != nil {
		log.Printf("failed to classify intent via LLM: %v", err)
		r.replyError(channelID, userID, "I couldn't understand your request. Try: `/ovad debug the latest message` or `/ovad add env var KEY=VALUE in file.tf in my-repo repository`")
		return
	}

	classification := strings.TrimSpace(strings.ToLower(result))

	switch {
	case strings.Contains(classification, "debug"):
		handler := &DebugHandler{slackClient: r.slackClient, modelsClient: r.modelsClient}
		handler.Execute(channelID, userID, text)
	case strings.Contains(classification, "filemod"):
		handler := &FileModHandler{slackClient: r.slackClient, ghClient: r.ghClient, modelsClient: r.modelsClient}
		handler.Execute(channelID, userID, text)
	default:
		r.replyError(channelID, userID, "I couldn't determine what you want. Try: `/ovad debug the latest message` or `/ovad add env var KEY=VALUE in file.tf in my-repo repository`")
	}
}

func (r *Router) replyError(channelID, userID, msg string) {
	if err := r.slackClient.PostEphemeral(channelID, userID, msg); err != nil {
		log.Printf("failed to send error to user: %v", err)
	}
}

func contextBackground() context.Context {
	return context.Background()
}
