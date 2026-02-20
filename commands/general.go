package commands

import (
	"context"
	"fmt"
	"log"

	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

type GeneralHandler struct {
	modelsClient *github.ModelsClient
}

func (h *GeneralHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	systemPrompt := `You are ovad, a helpful DevOps and engineering assistant running inside Slack.
You can answer general questions about DevOps, cloud infrastructure, CI/CD, Kubernetes, Terraform, GitHub, and software engineering.
Keep answers concise and actionable. Use Slack-compatible markdown formatting.
If the question requires performing an action you cannot do (like listing repositories or deploying), explain what the user can do and suggest using specific ovad commands if applicable.
Available ovad commands:
- Debug: analyze recent channel messages for errors/alerts (e.g., "/ovad debug the latest messages")
- File modification: modify files in GitHub repos via PR (e.g., "/ovad add env var KEY=VALUE in file.tf in my-repo repository")`

	response, err := h.modelsClient.Complete(ctx, systemPrompt, text)
	if err != nil {
		log.Printf("[user=%s channel=%s] LLM completion failed for general query: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to process request: %v", err), true)
		return
	}

	log.Printf("[user=%s channel=%s] general query completed successfully", userID, channelID)
	if err := ovadslack.RespondToURL(responseURL, response, false); err != nil {
		log.Printf("[user=%s channel=%s] failed to post general response: %v", userID, channelID, err)
	}
}
