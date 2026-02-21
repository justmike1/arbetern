package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

const maxToolRounds = 5

type GeneralHandler struct {
	ghClient        *github.Client
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
	memory          *ConversationMemory
}

func (h *GeneralHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	tools := h.buildTools()

	channelContext := ""
	if cc, err := h.contextProvider.GetChannelContext(channelID); err == nil {
		channelContext = cc
	}

	systemMsg := h.systemPrompt()
	history := h.memory.GetHistory(channelID, userID)
	if history != "" {
		systemMsg += fmt.Sprintf("\n\nPrevious conversation with this user:\n%s", history)
	}
	if channelContext != "" && channelContext != "(no recent messages)" {
		systemMsg += fmt.Sprintf("\n\nRecent channel messages for context:\n%s", channelContext)
	}

	messages := []github.ChatMessage{
		github.NewChatMessage("system", systemMsg),
		github.NewChatMessage("user", text),
	}

	for i := 0; i < maxToolRounds; i++ {
		resp, err := h.modelsClient.CompleteWithTools(ctx, messages, tools)
		if err != nil {
			log.Printf("[user=%s channel=%s] LLM completion failed for general query: %v", userID, channelID, err)
			_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to process request: %v", err), true)
			return
		}

		if len(resp.Choices) == 0 {
			log.Printf("[user=%s channel=%s] LLM returned no choices", userID, channelID)
			_ = ovadslack.RespondToURL(responseURL, "No response from the model.", true)
			return
		}

		choice := resp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			log.Printf("[user=%s channel=%s] general query completed successfully", userID, channelID)
			h.memory.SetAssistantResponse(channelID, userID, choice.Message.Content)
			if err := ovadslack.RespondToURL(responseURL, choice.Message.Content, false); err != nil {
				log.Printf("[user=%s channel=%s] failed to post general response: %v", userID, channelID, err)
			}
			return
		}

		messages = append(messages, github.ChatMessage{
			Role:      "assistant",
			ToolCalls: choice.Message.ToolCalls,
		})

		for _, tc := range choice.Message.ToolCalls {
			log.Printf("[user=%s channel=%s] LLM called tool: %s(%s)", userID, channelID, tc.Function.Name, tc.Function.Arguments)
			result := h.executeTool(ctx, channelID, userID, tc.Function.Name, tc.Function.Arguments)
			messages = append(messages, github.NewToolResultMessage(tc.ID, result))
		}
	}

	log.Printf("[user=%s channel=%s] exceeded max tool rounds", userID, channelID)
	_ = ovadslack.RespondToURL(responseURL, "The request required too many steps. Please try a simpler query.", true)
}

func (h *GeneralHandler) systemPrompt() string {
	return prompts.MustGet("general")
}

func (h *GeneralHandler) buildTools() []github.Tool {
	return []github.Tool{
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "list_org_repos",
				Description: "List all repositories in the GitHub organization that the bot has access to.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "list_user_repos",
				Description: "List all repositories accessible by the authenticated GitHub user.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "get_file_content",
				Description: "Read the content of a file from a GitHub repository.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"path":{"type":"string","description":"File path within the repository"},
						"branch":{"type":"string","description":"Branch name (optional, uses default branch if empty)"}
					},
					"required":["repo","path"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "get_repo_default_branch",
				Description: "Get the default branch name of a repository.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"}
					},
					"required":["repo"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "get_authenticated_user",
				Description: "Get the GitHub username of the authenticated bot user.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "resolve_owner",
				Description: "Resolve the GitHub organization or user that owns repositories.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "fetch_channel_context",
				Description: "Fetch recent messages from the current Slack channel for additional context about the ongoing conversation.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
	}
}

func (h *GeneralHandler) executeTool(ctx context.Context, channelID, userID, name, argsJSON string) string {
	switch name {
	case "list_org_repos":
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		repos, err := h.ghClient.ListOrgRepos(ctx, owner)
		if err != nil {
			return fmt.Sprintf("Error listing org repos: %v", err)
		}
		if len(repos) == 0 {
			return fmt.Sprintf("No repositories found for organization %s.", owner)
		}
		log.Printf("[user=%s channel=%s] listed %d org repos for %s", userID, channelID, len(repos), owner)
		return fmt.Sprintf("Organization: %s\nRepositories (%d):\n%s", owner, len(repos), strings.Join(repos, "\n"))

	case "list_user_repos":
		repos, err := h.ghClient.ListUserRepos(ctx)
		if err != nil {
			return fmt.Sprintf("Error listing user repos: %v", err)
		}
		if len(repos) == 0 {
			return "No repositories found for the authenticated user."
		}
		log.Printf("[user=%s channel=%s] listed %d user repos", userID, channelID, len(repos))
		return fmt.Sprintf("Repositories (%d):\n%s", len(repos), strings.Join(repos, "\n"))

	case "get_file_content":
		var args struct {
			Repo   string `json:"repo"`
			Path   string `json:"path"`
			Branch string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		branch := args.Branch
		if branch == "" {
			branch, err = h.ghClient.GetDefaultBranch(ctx, owner, args.Repo)
			if err != nil {
				return fmt.Sprintf("Error getting default branch: %v", err)
			}
		}
		content, _, err := h.ghClient.GetFileContent(ctx, owner, args.Repo, args.Path, branch)
		if err != nil {
			return fmt.Sprintf("Error reading file: %v", err)
		}
		if len(content) > 3000 {
			content = content[:3000] + "\n... (truncated)"
		}
		return content

	case "get_repo_default_branch":
		var args struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		branch, err := h.ghClient.GetDefaultBranch(ctx, owner, args.Repo)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return fmt.Sprintf("Default branch for %s: %s", args.Repo, branch)

	case "get_authenticated_user":
		user, err := h.ghClient.GetAuthenticatedUser(ctx)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return fmt.Sprintf("Authenticated as: %s", user)

	case "resolve_owner":
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return fmt.Sprintf("Resolved owner: %s", owner)

	case "fetch_channel_context":
		context, err := h.contextProvider.GetChannelContext(channelID)
		if err != nil {
			return fmt.Sprintf("Error fetching channel context: %v", err)
		}
		log.Printf("[user=%s channel=%s] fetched channel context via tool", userID, channelID)
		return context

	default:
		return fmt.Sprintf("Unknown tool: %s", name)
	}
}
