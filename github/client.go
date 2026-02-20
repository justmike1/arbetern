package github

import (
	"context"
	"encoding/base64"
	"fmt"
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
