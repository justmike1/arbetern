package commands

import (
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
	case isIntroIntent(lower):
		log.Printf("[user=%s channel=%s] routed to: intro", userID, channelID)
		_ = ovadslack.RespondToURL(responseURL, prompts.MustGet("intro"), false)
		return

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

	default:
		log.Printf("[user=%s channel=%s] routed to: general handler", userID, channelID)
		handler := &GeneralHandler{ghClient: r.ghClient, modelsClient: r.modelsClient, contextProvider: r.contextProvider, memory: r.memory}
		handler.Execute(channelID, userID, text, responseURL)
	}
}

func isIntroIntent(text string) bool {
	// Exact-match keywords — the entire message must be exactly this.
	exactKeywords := []string{"help", "hi", "hello"}
	trimmed := strings.TrimSpace(text)
	for _, kw := range exactKeywords {
		if trimmed == kw {
			return true
		}
	}
	// Substring-match keywords — safe because they are multi-word and specific.
	substringKeywords := []string{"introduce yourself", "who are you", "what are you", "what can you do", "what do you do"}
	for _, kw := range substringKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
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

func (r *Router) replyError(responseURL, msg string) {
	if err := ovadslack.RespondToURL(responseURL, msg, true); err != nil {
		log.Printf("failed to send error to user: %v", err)
	}
}
