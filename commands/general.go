package commands

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

type GeneralHandler struct {
	ghClient     *github.Client
	modelsClient *github.ModelsClient
}

func (h *GeneralHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()
	lower := strings.ToLower(text)

	if isListReposIntent(lower) {
		h.handleListRepos(ctx, channelID, userID, text, responseURL)
		return
	}

	h.handleGenericQuestion(ctx, channelID, userID, text, responseURL)
}

func isListReposIntent(text string) bool {
	return (strings.Contains(text, "list") || strings.Contains(text, "show") || strings.Contains(text, "all")) &&
		(strings.Contains(text, "repo") || strings.Contains(text, "repositories"))
}

func (h *GeneralHandler) handleListRepos(ctx context.Context, channelID, userID, text, responseURL string) {
	owner, err := h.ghClient.ResolveOwner(ctx)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to resolve owner for listing repos: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to determine organization: %v", err), true)
		return
	}

	repos, err := h.ghClient.ListOrgRepos(ctx, owner)
	if err != nil {
		log.Printf("[user=%s channel=%s] org repo list failed, trying user repos: %v", userID, channelID, err)
		repos, err = h.ghClient.ListUserRepos(ctx)
		if err != nil {
			log.Printf("[user=%s channel=%s] failed to list user repos: %v", userID, channelID, err)
			_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to list repositories: %v", err), true)
			return
		}
	}

	if len(repos) == 0 {
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("No repositories found for *%s*.", owner), false)
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "*Repositories for %s* (%d total):\n", owner, len(repos))
	for _, repo := range repos {
		fmt.Fprintf(&sb, "- `%s`\n", repo)
	}

	log.Printf("[user=%s channel=%s] listed %d repositories for %s", userID, channelID, len(repos), owner)
	if err := ovadslack.RespondToURL(responseURL, sb.String(), false); err != nil {
		log.Printf("[user=%s channel=%s] failed to post repo list: %v", userID, channelID, err)
	}
}

func (h *GeneralHandler) handleGenericQuestion(ctx context.Context, channelID, userID, text, responseURL string) {
	systemPrompt := `You are ovad, a helpful DevOps and engineering assistant running inside Slack.
You can answer general questions about DevOps, cloud infrastructure, CI/CD, Kubernetes, Terraform, GitHub, and software engineering.
Keep answers concise and actionable. Use Slack-compatible markdown formatting.
Available ovad commands:
- Debug: analyze recent channel messages for errors/alerts (e.g., "/ovad debug the latest messages")
- File modification: modify files in GitHub repos via PR (e.g., "/ovad add env var KEY=VALUE in file.tf in my-repo repository")
- List repositories: list all repos in the organization (e.g., "/ovad list all repositories")`

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
