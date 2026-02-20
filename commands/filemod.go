package commands

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

type FileModHandler struct {
	slackClient     SlackClient
	ghClient        *github.Client
	modelsClient    *github.ModelsClient
	contextProvider *ContextProvider
	memory          *ConversationMemory
}

type fileModParams struct {
	Repository  string
	FilePath    string
	Description string
}

var repoPattern = regexp.MustCompile(`(?i)in\s+(\S+)\s+repository`)
var filePattern = regexp.MustCompile(`(?i)in\s+(\S+\.\w+)`)

func (h *FileModHandler) Execute(channelID, userID, text, responseURL string) {
	ctx := context.Background()

	params, err := h.parseParams(ctx, text)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to parse file modification params: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Could not understand the request: %v", err), true)
		return
	}

	owner, err := h.ghClient.ResolveOwner(ctx)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to resolve owner: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to determine repository owner: %v", err), true)
		return
	}

	defaultBranch, err := h.ghClient.GetDefaultBranch(ctx, owner, params.Repository)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to get default branch for %s/%s: %v", userID, channelID, owner, params.Repository, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to access repository %s/%s: %v", owner, params.Repository, err), true)
		return
	}

	currentContent, fileSHA, err := h.ghClient.GetFileContent(ctx, owner, params.Repository, params.FilePath, defaultBranch)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to get file %s from %s/%s: %v", userID, channelID, params.FilePath, owner, params.Repository, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to read file %s: %v", params.FilePath, err), true)
		return
	}

	systemPrompt := prompts.MustGet("filemod")

	userPrompt := fmt.Sprintf("Current file (%s):\n\n%s\n\nRequested change: %s", params.FilePath, currentContent, params.Description)

	newContent, err := h.modelsClient.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		log.Printf("[user=%s channel=%s] LLM completion failed for filemod: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to generate file modification: %v", err), true)
		return
	}

	branchName := github.GenerateBranchName("filemod")

	if err := h.ghClient.CreateBranch(ctx, owner, params.Repository, defaultBranch, branchName); err != nil {
		log.Printf("[user=%s channel=%s] failed to create branch %s: %v", userID, channelID, branchName, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to create branch: %v", err), true)
		return
	}

	commitMsg := fmt.Sprintf("ovad: %s", params.Description)
	if err := h.ghClient.UpdateFile(ctx, owner, params.Repository, params.FilePath, branchName, commitMsg, []byte(newContent), fileSHA); err != nil {
		log.Printf("[user=%s channel=%s] failed to commit file %s to %s: %v", userID, channelID, params.FilePath, branchName, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Failed to commit changes: %v", err), true)
		return
	}

	prTitle := fmt.Sprintf("ovad: %s", params.Description)
	prBody := fmt.Sprintf("Automated change requested via Slack by <@%s>.\n\nRequest: %s", userID, text)

	prURL, err := h.ghClient.CreatePullRequest(ctx, owner, params.Repository, defaultBranch, branchName, prTitle, prBody)
	if err != nil {
		log.Printf("[user=%s channel=%s] failed to create PR: %v", userID, channelID, err)
		_ = ovadslack.RespondToURL(responseURL, fmt.Sprintf("Changes were committed to branch `%s` but PR creation failed: %v", branchName, err), true)
		return
	}

	log.Printf("[user=%s channel=%s] PR created: %s", userID, channelID, prURL)
	msg := fmt.Sprintf("Pull request created: %s", prURL)
	h.memory.SetAssistantResponse(channelID, userID, msg)
	if err := ovadslack.RespondToURL(responseURL, msg, false); err != nil {
		log.Printf("[user=%s channel=%s] failed to post PR link: %v", userID, channelID, err)
	}
}

func (h *FileModHandler) parseParams(ctx context.Context, text string) (*fileModParams, error) {
	repoMatch := repoPattern.FindStringSubmatch(text)
	fileMatch := filePattern.FindStringSubmatch(text)

	if repoMatch != nil && fileMatch != nil {
		return &fileModParams{
			Repository:  repoMatch[1],
			FilePath:    fileMatch[1],
			Description: text,
		}, nil
	}

	return h.parseParamsWithLLM(ctx, text)
}

func (h *FileModHandler) parseParamsWithLLM(ctx context.Context, text string) (*fileModParams, error) {
	systemPrompt := prompts.MustGet("filemod_parser")

	result, err := h.modelsClient.Complete(ctx, systemPrompt, text)
	if err != nil {
		return nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	params := &fileModParams{}
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "repository:") {
			params.Repository = strings.TrimSpace(strings.TrimPrefix(line, "repository:"))
		} else if strings.HasPrefix(line, "filepath:") {
			params.FilePath = strings.TrimSpace(strings.TrimPrefix(line, "filepath:"))
		} else if strings.HasPrefix(line, "description:") {
			params.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}

	if params.Repository == "" || params.FilePath == "" {
		return nil, fmt.Errorf("could not extract repository and file path from request")
	}
	if params.Description == "" {
		params.Description = text
	}

	return params, nil
}
