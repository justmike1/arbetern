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

const maxToolRounds = 20

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
	systemMsg = strings.Replace(systemMsg, "{{MODEL}}", h.modelsClient.Model(), 1)
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
	return prompts.MustGet("security") + "\n\n" + prompts.MustGet("general")
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
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "search_files",
				Description: "Search for files in a repository by name or path pattern. Returns all file paths containing the search term. Use this FIRST when looking for a specific file — it is much faster than navigating directories one by one.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"pattern":{"type":"string","description":"Search term to match against file paths (case-insensitive, e.g. 'services.yaml' or 'amplify/main.tf')"},
						"branch":{"type":"string","description":"Branch name (optional, uses default branch if empty)"}
					},
					"required":["repo","pattern"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "list_directory",
				Description: "List the files and subdirectories at a path in a GitHub repository. Use this when get_file_content fails because a path is a directory, or when you need to discover what files exist under a path.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"path":{"type":"string","description":"Directory path within the repository"},
						"branch":{"type":"string","description":"Branch name (optional, uses default branch if empty)"}
					},
					"required":["repo","path"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "modify_file",
				Description: "Modify a file in a GitHub repository by providing the complete new file content. This creates a new branch, commits the change, and creates a pull request. Use this when the user asks to change, update, add, or edit something in a file. You must first read the file with get_file_content, apply the requested changes yourself, then call this tool with the full updated content.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"path":{"type":"string","description":"File path within the repository"},
						"new_content":{"type":"string","description":"The complete new file content with the changes applied"},
						"description":{"type":"string","description":"Short description of what was changed (used as commit message and PR title)"},
						"branch":{"type":"string","description":"Base branch name (optional, uses default branch if empty)"}
					},
					"required":["repo","path","new_content","description"]
				}`),
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
			hint := ""
			if strings.Contains(err.Error(), "404") {
				hint = " This path may be a directory, or it may be nested under a provider subdirectory (e.g. aws/, azure/). Try list_directory on the parent path to discover the correct structure, then read the files you need."
			}
			return fmt.Sprintf("Error reading file: %v.%s", err, hint)
		}
		if len(content) > 8000 {
			content = content[:8000] + "\n... (truncated — file is longer than shown, important content may follow)"
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

	case "search_files":
		var args struct {
			Repo    string `json:"repo"`
			Pattern string `json:"pattern"`
			Branch  string `json:"branch"`
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
		matches, err := h.ghClient.SearchFiles(ctx, owner, args.Repo, branch, args.Pattern)
		if err != nil {
			return fmt.Sprintf("Error searching files: %v", err)
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No files matching '%s' found in %s.", args.Pattern, args.Repo)
		}
		log.Printf("[user=%s channel=%s] searched files in %s for '%s' (%d matches)", userID, channelID, args.Repo, args.Pattern, len(matches))
		if len(matches) > 50 {
			matches = matches[:50]
			return fmt.Sprintf("Found %d+ matches (showing first 50):\n%s", len(matches), strings.Join(matches, "\n"))
		}
		return fmt.Sprintf("Found %d matches:\n%s", len(matches), strings.Join(matches, "\n"))

	case "list_directory":
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
		entries, err := h.ghClient.GetDirectoryContents(ctx, owner, args.Repo, args.Path, branch)
		if err != nil {
			return fmt.Sprintf("Error listing directory: %v", err)
		}
		log.Printf("[user=%s channel=%s] listed directory %s/%s/%s (%d entries)", userID, channelID, args.Repo, branch, args.Path, len(entries))
		return fmt.Sprintf("Contents of %s/%s:\n%s", args.Repo, args.Path, strings.Join(entries, "\n"))

	case "fetch_channel_context":
		context, err := h.contextProvider.GetChannelContext(channelID)
		if err != nil {
			return fmt.Sprintf("Error fetching channel context: %v", err)
		}
		log.Printf("[user=%s channel=%s] fetched channel context via tool", userID, channelID)
		return context

	case "modify_file":
		var args struct {
			Repo        string `json:"repo"`
			Path        string `json:"path"`
			NewContent  string `json:"new_content"`
			Description string `json:"description"`
			Branch      string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		baseBranch := args.Branch
		if baseBranch == "" {
			baseBranch, err = h.ghClient.GetDefaultBranch(ctx, owner, args.Repo)
			if err != nil {
				return fmt.Sprintf("Error getting default branch: %v", err)
			}
		}
		_, fileSHA, err := h.ghClient.GetFileContent(ctx, owner, args.Repo, args.Path, baseBranch)
		if err != nil {
			return fmt.Sprintf("Error reading current file for SHA: %v", err)
		}
		branchName := github.GenerateBranchName("ovad")
		if err := h.ghClient.CreateBranch(ctx, owner, args.Repo, baseBranch, branchName); err != nil {
			return fmt.Sprintf("Error creating branch: %v", err)
		}
		commitMsg := fmt.Sprintf("ovad: %s", args.Description)
		if err := h.ghClient.UpdateFile(ctx, owner, args.Repo, args.Path, branchName, commitMsg, []byte(args.NewContent), fileSHA); err != nil {
			return fmt.Sprintf("Error committing file: %v", err)
		}
		prTitle := fmt.Sprintf("ovad: %s", args.Description)
		prBody := fmt.Sprintf("Automated change requested via Slack by <@%s>.\n\nChange: %s", userID, args.Description)
		prURL, err := h.ghClient.CreatePullRequest(ctx, owner, args.Repo, baseBranch, branchName, prTitle, prBody)
		if err != nil {
			return fmt.Sprintf("Changes committed to branch %s but PR creation failed: %v", branchName, err)
		}
		log.Printf("[user=%s channel=%s] PR created via modify_file: %s", userID, channelID, prURL)
		return fmt.Sprintf("Pull request created: %s", prURL)

	default:
		return fmt.Sprintf("Unknown tool: %s", name)
	}
}
