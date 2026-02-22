package commands

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

type Router struct {
	slackClient     SlackClient
	ghClient        *github.Client
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
	memory          *ConversationMemory
}

func NewRouter(slackClient SlackClient, ghClient *github.Client, modelsClient *github.ModelsClient) *Router {
	return &Router{
		slackClient:     slackClient,
		ghClient:        ghClient,
		modelsClient:    modelsClient,
		contextProvider: NewContextProvider(slackClient),
		memory:          NewConversationMemory(),
	}
}

func (r *Router) Handle(channelID, userID, text, responseURL string) {
	text = strings.TrimSpace(text)
	if text == "" {
		log.Printf("[user=%s channel=%s] empty command received", userID, channelID)
		r.replyError(responseURL, "Please provide a command. Example: `/ovad please debug the latest message in this channel`")
		return
	}

	log.Printf("[user=%s channel=%s] received command: %s", userID, channelID, text)

	auditMsg := fmt.Sprintf(":mag: <@%s> requested in <#%s>:\n> %s", userID, channelID, text)
	if err := r.slackClient.PostMessage(channelID, auditMsg); err != nil {
		log.Printf("[user=%s channel=%s] failed to post audit message: %v", userID, channelID, err)
	}

	_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Processing request: _%s_", text), true)

	r.memory.AddUserMessage(channelID, userID, text)

	lower := strings.ToLower(text)

	switch {
	case isDebugIntent(lower):
		log.Printf("[user=%s channel=%s] routed to: debug", userID, channelID)
		handler := &DebugHandler{
			slackClient:     r.slackClient,
			ghClient:        r.ghClient,
			modelsClient:    r.modelsClient,
			contextProvider: r.contextProvider,
			memory:          r.memory,
		}
		handler.Execute(channelID, userID, text, responseURL)

	case isFileModIntent(lower):
		log.Printf("[user=%s channel=%s] routed to: filemod", userID, channelID)
		handler := &FileModHandler{
			slackClient:     r.slackClient,
			ghClient:        r.ghClient,
			modelsClient:    r.modelsClient,
			contextProvider: r.contextProvider,
			memory:          r.memory,
		}
		handler.Execute(channelID, userID, text, responseURL)

	default:
		log.Printf("[user=%s channel=%s] routed to: LLM classification", userID, channelID)
		r.handleAmbiguous(channelID, userID, text, responseURL)
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
	modKeywords := []string{"add env", "modify", "update file", "change file", "edit file", "add variable"}
	for _, kw := range modKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func (r *Router) handleAmbiguous(channelID, userID, text, responseURL string) {
	ctx := contextBackground()

	systemPrompt := prompts.MustGet("classifier")
	history := r.memory.GetHistory(channelID, userID)

	classifyInput := text
	if history != "" {
		classifyInput = fmt.Sprintf("Previous conversation:\n%s\n\nCurrent message: %s", history, text)
	}

	result, err := r.modelsClient.Complete(ctx, systemPrompt, classifyInput)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to classify intent via LLM: %v", userID, channelID, err)
		r.replyError(responseURL, "I couldn't understand your request. Try: `/ovad debug the latest message` or `/ovad add env var KEY=VALUE in file.tf in my-repo repository`")
		return
	}

	classification := strings.TrimSpace(strings.ToLower(result))
	log.Printf("[user=%s channel=%s] LLM classified intent as: %s", userID, channelID, classification)

	switch {
	case strings.Contains(classification, "debug"):
		handler := &DebugHandler{slackClient: r.slackClient, ghClient: r.ghClient, modelsClient: r.modelsClient, contextProvider: r.contextProvider, memory: r.memory}
		handler.Execute(channelID, userID, text, responseURL)
	case strings.Contains(classification, "filemod"):
		handler := &FileModHandler{slackClient: r.slackClient, ghClient: r.ghClient, modelsClient: r.modelsClient, contextProvider: r.contextProvider, memory: r.memory}
		handler.Execute(channelID, userID, text, responseURL)
	default:
		log.Printf("[user=%s channel=%s] routed to: general handler", userID, channelID)
		handler := &GeneralHandler{ghClient: r.ghClient, modelsClient: r.modelsClient, contextProvider: r.contextProvider, memory: r.memory}
		handler.Execute(channelID, userID, text, responseURL)
	}
}

func (r *Router) replyError(responseURL, msg string) {
	if err := ovadslack.RespondToURL(responseURL, msg, true); err != nil {
		log.Printf("failed to send error to user: %v", err)
	}
}

func contextBackground() context.Context {
	return context.Background()
}
