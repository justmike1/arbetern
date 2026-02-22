package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	gh "github.com/google/go-github/v60/github"
	"golang.org/x/oauth2"
)

type Client struct {
	api *gh.Client
}

func NewClient(token string) *Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), ts)
	return &Client{api: gh.NewClient(httpClient)}
}

func (c *Client) GetAuthenticatedUser(ctx context.Context) (string, error) {
	user, _, err := c.api.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("failed to get authenticated user: %w", err)
	}
	return user.GetLogin(), nil
}

func (c *Client) ResolveOwner(ctx context.Context) (string, error) {
	user, _, err := c.api.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("failed to resolve owner: %w", err)
	}

	orgs, _, err := c.api.Organizations.List(ctx, "", nil)
	if err == nil && len(orgs) > 0 {
		return orgs[0].GetLogin(), nil
	}

	return user.GetLogin(), nil
}

func (c *Client) GetFileContent(ctx context.Context, owner, repo, path, branch string) (string, string, error) {
	opts := &gh.RepositoryContentGetOptions{Ref: branch}
	file, _, _, err := c.api.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return "", "", fmt.Errorf("failed to get file %s: %w", path, err)
	}

	content, err := base64.StdEncoding.DecodeString(*file.Content)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode file content: %w", err)
	}

	return string(content), file.GetSHA(), nil
}

func (c *Client) GetDefaultBranch(ctx context.Context, owner, repo string) (string, error) {
	r, _, err := c.api.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("failed to get repository %s/%s: %w", owner, repo, err)
	}
	return r.GetDefaultBranch(), nil
}

func (c *Client) CreateBranch(ctx context.Context, owner, repo, baseBranch, newBranch string) error {
	ref, _, err := c.api.Git.GetRef(ctx, owner, repo, "refs/heads/"+baseBranch)
	if err != nil {
		return fmt.Errorf("failed to get ref for %s: %w", baseBranch, err)
	}

	newRef := &gh.Reference{
		Ref:    gh.String("refs/heads/" + newBranch),
		Object: ref.Object,
	}

	_, _, err = c.api.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		return fmt.Errorf("failed to create branch %s: %w", newBranch, err)
	}
	return nil
}

func (c *Client) UpdateFile(ctx context.Context, owner, repo, path, branch, message string, content []byte, sha string) error {
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.String(message),
		Content: content,
		Branch:  gh.String(branch),
		SHA:     gh.String(sha),
	}

	_, _, err := c.api.Repositories.UpdateFile(ctx, owner, repo, path, opts)
	if err != nil {
		return fmt.Errorf("failed to update file %s: %w", path, err)
	}
	return nil
}

func (c *Client) CreatePullRequest(ctx context.Context, owner, repo, baseBranch, headBranch, title, body string) (string, error) {
	pr := &gh.NewPullRequest{
		Title: gh.String(title),
		Body:  gh.String(body),
		Head:  gh.String(headBranch),
		Base:  gh.String(baseBranch),
	}

	created, _, err := c.api.PullRequests.Create(ctx, owner, repo, pr)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}
	return created.GetHTMLURL(), nil
}

func GenerateBranchName(prefix string) string {
	return fmt.Sprintf("ovad/%s-%d", prefix, time.Now().Unix())
}

func (c *Client) SearchFiles(ctx context.Context, owner, repo, branch, pattern string) ([]string, error) {
	ref, _, err := c.api.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get ref for %s: %w", branch, err)
	}
	tree, _, err := c.api.Git.GetTree(ctx, owner, repo, ref.Object.GetSHA(), true)
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %w", err)
	}
	lowerPattern := strings.ToLower(pattern)
	var matches []string
	for _, entry := range tree.Entries {
		path := entry.GetPath()
		if strings.Contains(strings.ToLower(path), lowerPattern) {
			matches = append(matches, path)
		}
	}
	return matches, nil
}

func (c *Client) GetDirectoryContents(ctx context.Context, owner, repo, path, branch string) ([]string, error) {
	opts := &gh.RepositoryContentGetOptions{Ref: branch}
	_, dir, _, err := c.api.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get directory %s: %w", path, err)
	}
	if dir == nil {
		return nil, fmt.Errorf("path %s is not a directory", path)
	}
	var entries []string
	for _, entry := range dir {
		name := entry.GetPath()
		if entry.GetType() == "dir" {
			name += "/"
		}
		entries = append(entries, name)
	}
	return entries, nil
}

func (c *Client) ListOrgRepos(ctx context.Context, org string) ([]string, error) {
	var allRepos []string
	opts := &gh.RepositoryListByOrgOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := c.api.Repositories.ListByOrg(ctx, org, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories for org %s: %w", org, err)
		}
		for _, r := range repos {
			allRepos = append(allRepos, r.GetFullName())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allRepos, nil
}

func (c *Client) ListUserRepos(ctx context.Context) ([]string, error) {
	var allRepos []string
	opts := &gh.RepositoryListByAuthenticatedUserOptions{
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		repos, resp, err := c.api.Repositories.ListByAuthenticatedUser(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories: %w", err)
		}
		for _, r := range repos {
			allRepos = append(allRepos, r.GetFullName())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allRepos, nil
}

var workflowRunURLPattern = regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+)/actions/runs/(\d+)`)

func ParseWorkflowRunURL(rawURL string) (owner, repo string, runID int64, err error) {
	matches := workflowRunURLPattern.FindStringSubmatch(rawURL)
	if len(matches) != 4 {
		return "", "", 0, fmt.Errorf("not a valid GitHub Actions workflow run URL: %s", rawURL)
	}
	runID, err = strconv.ParseInt(matches[3], 10, 64)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid run ID in URL: %w", err)
	}
	return matches[1], matches[2], runID, nil
}

func ExtractWorkflowRunURLs(text string) []string {
	return workflowRunURLPattern.FindAllString(text, -1)
}

type WorkflowRunSummary struct {
	RunID       int64
	Name        string
	Status      string
	Conclusion  string
	URL         string
	Jobs        []WorkflowJobSummary
	Annotations []WorkflowAnnotation
}

type WorkflowJobSummary struct {
	Name       string
	Status     string
	Conclusion string
	Steps      []WorkflowStepSummary
}

type WorkflowStepSummary struct {
	Name       string
	Status     string
	Conclusion string
}

type WorkflowAnnotation struct {
	JobName string
	Level   string
	Message string
	Title   string
}

func (c *Client) GetWorkflowRunSummary(ctx context.Context, owner, repo string, runID int64) (*WorkflowRunSummary, error) {
	run, _, err := c.api.Actions.GetWorkflowRunByID(ctx, owner, repo, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow run %d: %w", runID, err)
	}

	summary := &WorkflowRunSummary{
		RunID:      runID,
		Name:       run.GetName(),
		Status:     run.GetStatus(),
		Conclusion: run.GetConclusion(),
		URL:        run.GetHTMLURL(),
	}

	jobs, _, err := c.api.Actions.ListWorkflowJobs(ctx, owner, repo, runID, nil)
	if err != nil {
		return summary, fmt.Errorf("failed to list jobs for run %d: %w", runID, err)
	}

	for _, job := range jobs.Jobs {
		js := WorkflowJobSummary{
			Name:       job.GetName(),
			Status:     job.GetStatus(),
			Conclusion: job.GetConclusion(),
		}
		for _, step := range job.Steps {
			js.Steps = append(js.Steps, WorkflowStepSummary{
				Name:       step.GetName(),
				Status:     step.GetStatus(),
				Conclusion: step.GetConclusion(),
			})
		}
		summary.Jobs = append(summary.Jobs, js)

		checkRunID := parseCheckRunID(job.GetCheckRunURL())
		if checkRunID == 0 {
			continue
		}
		annotations, _, err := c.api.Checks.ListCheckRunAnnotations(ctx, owner, repo, checkRunID, nil)
		if err != nil {
			continue
		}
		for _, ann := range annotations {
			summary.Annotations = append(summary.Annotations, WorkflowAnnotation{
				JobName: job.GetName(),
				Level:   ann.GetAnnotationLevel(),
				Message: ann.GetMessage(),
				Title:   ann.GetTitle(),
			})
		}
	}

	return summary, nil
}

func parseCheckRunID(checkRunURL string) int64 {
	if checkRunURL == "" {
		return 0
	}
	parts := strings.Split(checkRunURL, "/")
	if len(parts) == 0 {
		return 0
	}
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func FormatWorkflowRunSummary(s *WorkflowRunSummary) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Workflow Run: %s (ID: %d)\n", s.Name, s.RunID)
	fmt.Fprintf(&sb, "Status: %s | Conclusion: %s\n", s.Status, s.Conclusion)
	fmt.Fprintf(&sb, "URL: %s\n\n", s.URL)

	for _, job := range s.Jobs {
		icon := "+"
		if job.Conclusion == "failure" {
			icon = "X"
		}
		fmt.Fprintf(&sb, "[%s] Job: %s (%s)\n", icon, job.Name, job.Conclusion)
		for _, step := range job.Steps {
			stepIcon := " "
			switch step.Conclusion {
			case "failure":
				stepIcon = "X"
			case "success":
				stepIcon = "+"
			}
			fmt.Fprintf(&sb, "  [%s] %s (%s)\n", stepIcon, step.Name, step.Conclusion)
		}
		sb.WriteString("\n")
	}

	if len(s.Annotations) > 0 {
		sb.WriteString("Annotations:\n")
		for _, ann := range s.Annotations {
			level := strings.ToUpper(ann.Level)
			fmt.Fprintf(&sb, "  [%s] %s\n", level, ann.JobName)
			if ann.Title != "" {
				fmt.Fprintf(&sb, "    Title: %s\n", ann.Title)
			}
			fmt.Fprintf(&sb, "    Message: %s\n", ann.Message)
		}
	}

	return sb.String()
}
