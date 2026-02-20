package commands

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/justmike1/ovad/github"
)

type FileModHandler struct {
	slackClient  SlackClient
	ghClient     *github.Client
	modelsClient *github.ModelsClient
}

type fileModParams struct {
	Repository  string
	FilePath    string
	Description string
}

var repoPattern = regexp.MustCompile(`(?i)in\s+(\S+)\s+repository`)
var filePattern = regexp.MustCompile(`(?i)in\s+(\S+\.\w+)`)

func (h *FileModHandler) Execute(channelID, userID, text string) {
	ctx := context.Background()

	params, err := h.parseParams(ctx, text)
	if err != nil {
		log.Printf("failed to parse file modification params: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Could not understand the request: %v", err))
		return
	}

	owner, err := h.ghClient.ResolveOwner(ctx)
	if err != nil {
		log.Printf("failed to resolve owner: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to determine repository owner: %v", err))
		return
	}

	defaultBranch, err := h.ghClient.GetDefaultBranch(ctx, owner, params.Repository)
	if err != nil {
		log.Printf("failed to get default branch: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to access repository %s/%s: %v", owner, params.Repository, err))
		return
	}

	currentContent, fileSHA, err := h.ghClient.GetFileContent(ctx, owner, params.Repository, params.FilePath, defaultBranch)
	if err != nil {
		log.Printf("failed to get file content: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to read file %s: %v", params.FilePath, err))
		return
	}

	systemPrompt := `You are an infrastructure-as-code expert. You will be given the current contents of a file and a modification request.
Return ONLY the complete updated file content with the requested changes applied. Do not include any explanation, markdown formatting, or code fences. Output the raw file content only.`

	userPrompt := fmt.Sprintf("Current file (%s):\n\n%s\n\nRequested change: %s", params.FilePath, currentContent, params.Description)

	newContent, err := h.modelsClient.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		log.Printf("LLM completion failed: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to generate file modification: %v", err))
		return
	}

	branchName := github.GenerateBranchName("filemod")

	if err := h.ghClient.CreateBranch(ctx, owner, params.Repository, defaultBranch, branchName); err != nil {
		log.Printf("failed to create branch: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to create branch: %v", err))
		return
	}

	commitMsg := fmt.Sprintf("ovad: %s", params.Description)
	if err := h.ghClient.UpdateFile(ctx, owner, params.Repository, params.FilePath, branchName, commitMsg, []byte(newContent), fileSHA); err != nil {
		log.Printf("failed to commit file: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Failed to commit changes: %v", err))
		return
	}

	prTitle := fmt.Sprintf("ovad: %s", params.Description)
	prBody := fmt.Sprintf("Automated change requested via Slack by <@%s>.\n\nRequest: %s", userID, text)

	prURL, err := h.ghClient.CreatePullRequest(ctx, owner, params.Repository, defaultBranch, branchName, prTitle, prBody)
	if err != nil {
		log.Printf("failed to create PR: %v", err)
		_ = h.slackClient.PostEphemeral(channelID, userID, fmt.Sprintf("Changes were committed to branch `%s` but PR creation failed: %v", branchName, err))
		return
	}

	msg := fmt.Sprintf("Pull request created: %s", prURL)
	if err := h.slackClient.PostMessage(channelID, msg); err != nil {
		log.Printf("failed to post PR link: %v", err)
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
	systemPrompt := `Extract file modification parameters from the user request. Respond in exactly this format (one per line, no extra text):
repository: <repo-name>
filepath: <file-path>
description: <what to change>`

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
