package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

const maxToolRounds = 20

type GeneralHandler struct {
	slackClient     SlackClient
	ghClient        *github.Client
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
	memory          *ConversationMemory
	prompts         PromptProvider
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
	return h.prompts.MustGet("security") + "\n\n" + h.prompts.MustGet("general")
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
				Description: "Modify a file in a GitHub repository using a safe find-and-replace approach. Provide the exact text to find (old_content) and the replacement text (new_content). The tool reads the FULL file from GitHub, performs the replacement, then creates a branch, commits, and opens a PR. IMPORTANT: old_content must be an exact substring of the current file — include enough surrounding lines (3-5) will ensure a unique match. Only the matched section is replaced; the rest of the file is preserved.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"path":{"type":"string","description":"File path within the repository"},
						"old_content":{"type":"string","description":"The exact text in the current file to find and replace. Include 3-5 surrounding context lines to ensure a unique match."},
						"new_content":{"type":"string","description":"The replacement text that will replace old_content."},
						"description":{"type":"string","description":"Short description of what was changed (used as commit message and PR title)"},
						"branch":{"type":"string","description":"Base branch name (optional, uses default branch if empty)"}
					},
					"required":["repo","path","old_content","new_content","description"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "get_pull_request",
				Description: "Get details, changed files, and diff of a GitHub pull request by number or URL. Use this to analyze what a PR changed, understand code patterns introduced or removed, and find old/new usage patterns.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"number":{"type":"integer","description":"Pull request number (e.g., 123)"},
						"url":{"type":"string","description":"Full GitHub PR URL (alternative to repo+number). If provided, repo and number are extracted from it."}
					},
					"required":[]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "list_pull_requests",
				Description: "List recent pull requests in a repository. Useful for finding relevant PRs by title, discovering recent changes, or identifying the PR that introduced a particular change.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"state":{"type":"string","description":"Filter by state: 'open', 'closed', or 'all' (default: 'all')"},
						"limit":{"type":"integer","description":"Maximum number of PRs to return (default: 10, max: 30)"}
					},
					"required":["repo"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "search_code",
				Description: "Search for code content within a GitHub repository. Unlike search_files (which matches file names/paths), this searches inside file contents. Use this to find usages of functions, classes, patterns, imports, or any code string across the entire repository. Returns matching files with code fragments showing the context around each match.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"repo":{"type":"string","description":"Repository name (without owner)"},
						"query":{"type":"string","description":"Code search query. Can include the code pattern to find (e.g., 'db.session', 'SessionLocal()', 'def create_session'). Supports GitHub code search qualifiers like 'language:python', 'path:src/', 'extension:py'."}
					},
					"required":["repo","query"]
				}`),
			},
		},
		{
			Type: "function",
			Function: github.ToolFunction{
				Name:        "reply_in_thread",
				Description: "Post a message as a threaded reply to a specific Slack message. Use this when the user asks you to reply inside someone's thread or respond to a particular message. You need the thread_ts of the target message, which you can find in the channel context output (each message includes a thread_ts value).",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"thread_ts":{"type":"string","description":"The thread_ts timestamp of the message to reply to (e.g., '1708700000.123456'). Get this from the channel context."},
						"text":{"type":"string","description":"The message text to post as a threaded reply. Supports Slack markdown formatting."}
					},
					"required":["thread_ts","text"]
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
			OldContent  string `json:"old_content"`
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
		fullContent, fileSHA, err := h.ghClient.GetFileContent(ctx, owner, args.Repo, args.Path, baseBranch)
		if err != nil {
			return fmt.Sprintf("Error reading current file: %v", err)
		}
		// Perform find-and-replace on the full file content.
		if !strings.Contains(fullContent, args.OldContent) {
			return "Error: old_content not found in the file. Make sure old_content is an exact substring of the current file (including whitespace and indentation). Re-read the file with get_file_content and try again."
		}
		occurrences := strings.Count(fullContent, args.OldContent)
		if occurrences > 1 {
			return fmt.Sprintf("Error: old_content matches %d locations in the file. Include more surrounding context lines to make it unique.", occurrences)
		}
		updatedContent := strings.Replace(fullContent, args.OldContent, args.NewContent, 1)
		branchName := github.GenerateBranchName("ovad")
		if err := h.ghClient.CreateBranch(ctx, owner, args.Repo, baseBranch, branchName); err != nil {
			return fmt.Sprintf("Error creating branch: %v", err)
		}
		commitMsg := fmt.Sprintf("ovad: %s", args.Description)
		if err := h.ghClient.UpdateFile(ctx, owner, args.Repo, args.Path, branchName, commitMsg, []byte(updatedContent), fileSHA); err != nil {
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

	case "get_pull_request":
		var args struct {
			Repo   string `json:"repo"`
			Number int    `json:"number"`
			URL    string `json:"url"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		// If a URL was provided, extract owner/repo/number from it.
		if args.URL != "" {
			prOwner, prRepo, prNum, parseErr := github.ParsePRURL(args.URL)
			if parseErr != nil {
				return fmt.Sprintf("Error parsing PR URL: %v", parseErr)
			}
			owner = prOwner
			args.Repo = prRepo
			args.Number = prNum
		}
		if args.Number == 0 {
			return "Error: PR number or URL is required."
		}
		pr, err := h.ghClient.GetPullRequest(ctx, owner, args.Repo, args.Number)
		if err != nil {
			return fmt.Sprintf("Error getting PR: %v", err)
		}
		log.Printf("[user=%s channel=%s] fetched PR #%d in %s/%s", userID, channelID, args.Number, owner, args.Repo)
		return github.FormatPRSummary(pr)

	case "list_pull_requests":
		var args struct {
			Repo  string `json:"repo"`
			State string `json:"state"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		prs, err := h.ghClient.ListPullRequests(ctx, owner, args.Repo, args.State, args.Limit)
		if err != nil {
			return fmt.Sprintf("Error listing PRs: %v", err)
		}
		if len(prs) == 0 {
			return fmt.Sprintf("No pull requests found in %s (state: %s).", args.Repo, args.State)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Pull Requests in %s (%d):\n", args.Repo, len(prs))
		for _, pr := range prs {
			fmt.Fprintf(&sb, "  • #%d %s (%s) by %s — %s\n", pr.Number, pr.Title, pr.State, pr.Author, pr.URL)
		}
		log.Printf("[user=%s channel=%s] listed %d PRs in %s", userID, channelID, len(prs), args.Repo)
		return sb.String()

	case "search_code":
		var args struct {
			Repo  string `json:"repo"`
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		owner, err := h.ghClient.ResolveOwner(ctx)
		if err != nil {
			return fmt.Sprintf("Error resolving owner: %v", err)
		}
		results, err := h.ghClient.SearchCode(ctx, owner, args.Repo, args.Query)
		if err != nil {
			return fmt.Sprintf("Error searching code: %v", err)
		}
		if len(results) == 0 {
			return fmt.Sprintf("No code matches found for '%s' in %s. Try different search terms, broader patterns, or check if the repository name is correct.", args.Query, args.Repo)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Code search results for '%s' in %s (%d matches):\n", args.Query, args.Repo, len(results))
		for _, r := range results {
			fmt.Fprintf(&sb, "\n• %s\n  %s\n", r.File, r.URL)
			for _, frag := range r.Fragments {
				fmt.Fprintf(&sb, "  ```\n  %s\n  ```\n", frag)
			}
		}
		log.Printf("[user=%s channel=%s] searched code in %s for '%s' (%d matches)", userID, channelID, args.Repo, args.Query, len(results))
		return sb.String()

	case "reply_in_thread":
		var args struct {
			ThreadTS string `json:"thread_ts"`
			Text     string `json:"text"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Error parsing arguments: %v", err)
		}
		if err := h.slackClient.PostThreadReply(channelID, args.ThreadTS, args.Text); err != nil {
			return fmt.Sprintf("Error posting thread reply: %v", err)
		}
		log.Printf("[user=%s channel=%s] posted thread reply to ts=%s", userID, channelID, args.ThreadTS)
		return "Successfully posted reply in thread."

	default:
		return fmt.Sprintf("Unknown tool: %s", name)
	}
}
