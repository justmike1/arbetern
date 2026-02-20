# OVAD - Slack Bot with GitHub Models Integration

## Overview

OVAD is a Slack bot that listens for slash commands in Slack channels, processes user requests using GitHub Models (LLMs), and performs actions such as debugging alert messages or modifying files in GitHub repositories. The bot is invoked via `/ovad` slash command.

---

## Architecture

```
Slack Channel
    |
    | (slash command / webhook)
    v
OVAD Go Server (HTTP webhook handler)
    |
    |--- Slack API Client (read channel history, post replies)
    |--- GitHub Models Client (LLM inference via GitHub Models API)
    |--- GitHub API Client (read/write repo files, resolve org/repo from token)
    |
    v
Response posted back to Slack thread
```

---

## Components

### 1. Configuration

- **File:** `config/config.go`
- Load configuration from environment variables:
  - `SLACK_BOT_TOKEN` - Slack Bot OAuth token (xoxb-...)
  - `SLACK_SIGNING_SECRET` - Slack request signing secret for webhook verification
  - `GITHUB_TOKEN` - GitHub personal access token (used for both GitHub Models API and GitHub API; org/repo access is derived from token permissions)
  - `GITHUB_MODEL` - Configurable model name (e.g., `openai/gpt-4o`, `meta/llama-3.1-405b-instruct`), with a sensible default
  - `PORT` - HTTP server port, default `8080`

### 2. Entry Point

- **File:** `main.go`
- Initialize configuration, clients, and HTTP server.
- Register the `/webhook` endpoint for Slack events.
- Start the HTTP server.

### 3. Slack Webhook Handler

- **File:** `slack/handler.go`
- Handle incoming POST requests from Slack slash commands.
- Verify request signature using `SLACK_SIGNING_SECRET`.
- Parse the slash command payload (extract `text`, `channel_id`, `user_id`, `response_url`).
- Respond with an immediate `200 OK` (Slack requires response within 3 seconds).
- Dispatch actual processing in a goroutine, then post the result back via `response_url` or Slack API.

### 4. Slack Client

- **File:** `slack/client.go`
- Wrapper around Slack Web API.
- Methods:
  - `FetchChannelHistory(channelID string, limit int)` - Retrieve recent messages from a channel.
  - `PostMessage(channelID string, text string)` - Post a response message to the channel.
  - `PostEphemeral(channelID, userID, text string)` - Post a message visible only to the requesting user if needed.

### 5. GitHub Models Client (LLM)

- **File:** `github/models.go`
- Call the GitHub Models inference API endpoint (`https://models.github.ai/inference/chat/completions`).
- Accept a system prompt and user prompt, return the model's response.
- The model name comes from configuration, making it swappable without code changes.
- Handle rate limits and errors gracefully.

### 6. GitHub API Client

- **File:** `github/client.go`
- Wrapper around GitHub REST/GraphQL API.
- Methods:
  - `GetAuthenticatedUser()` - Resolve the identity/org associated with the token.
  - `ListOrgRepos(org string)` - List available repositories (for validation).
  - `GetFileContent(owner, repo, path, branch string)` - Read a file from a repository.
  - `CreateCommitWithFileChange(owner, repo, path, branch, message string, content []byte)` - Update a file in a repository (create a commit).
  - `CreatePullRequest(owner, repo, baseBranch, headBranch, title, body string)` - Open a PR for the change.

### 7. Command Router / Intent Parser

- **File:** `commands/router.go`
- Take the raw text from the slash command and determine the user's intent.
- Two initial intents:
  - **Debug** - e.g., "please debug the latest message in this channel"
  - **File Modification** - e.g., "please add env var KEY=NAME in amplify.tf in devops-infra repository"
- Use a lightweight approach first (keyword matching / regex). If ambiguous, send the raw text to GitHub Models to classify the intent and extract parameters.

### 8. Command Handlers

#### 8a. Debug Handler

- **File:** `commands/debug.go`
- Flow:
  1. Fetch the last N messages from the Slack channel using the Slack client.
  2. Build a prompt for the LLM that includes the messages as context, asking it to analyze/debug the alert.
  3. Send the prompt to GitHub Models.
  4. Post the LLM response back to the Slack channel.

#### 8b. File Modification Handler

- **File:** `commands/filemod.go`
- Flow:
  1. Extract parameters from the parsed command: repository name, file path, and the desired change description.
  2. Resolve the organization from the GitHub token (via `GetAuthenticatedUser()`).
  3. Fetch the current file content from the repository.
  4. Build a prompt for the LLM: provide the current file content and the requested modification, ask it to return the updated file.
  5. Send to GitHub Models, receive the modified file content.
  6. Create a new branch, commit the change, and open a pull request.
  7. Post the PR link back to the Slack channel.

---

## Project Structure

```
ovad/
  main.go
  go.mod
  go.sum
  config/
    config.go
  slack/
    handler.go
    client.go
  github/
    models.go
    client.go
  commands/
    router.go
    debug.go
    filemod.go
```

---

## Dependencies

- `net/http` (stdlib) - HTTP server for webhook endpoint
- `github.com/slack-go/slack` - Slack API client library
- `github.com/google/go-github/v60/github` + `golang.org/x/oauth2` - GitHub API client
- No external LLM SDK; GitHub Models inference API is a standard OpenAI-compatible REST endpoint, called via `net/http` with JSON marshaling.

---

## Slash Command Setup in Slack

1. Create a Slack App at https://api.slack.com/apps.
2. Add a Slash Command `/ovad` pointing to the deployed server URL (e.g., `https://<host>/webhook`).
3. Enable the following bot token scopes:
   - `commands` (slash commands)
   - `channels:history` (read channel messages)
   - `chat:write` (post messages)
4. Install the app to the workspace and copy the Bot Token and Signing Secret.

---

## Request Flow (end to end)

1. User types `/ovad please debug the latest message in this channel` in a Slack channel.
2. Slack sends a POST to `https://<host>/webhook` with the command payload.
3. Server verifies the Slack signature, parses the command text.
4. Server responds with `200 OK` immediately (Slack timeout constraint).
5. In a background goroutine:
   a. Router determines intent: **debug**.
   b. Debug handler fetches last N messages from that channel via Slack API.
   c. Constructs a prompt with the channel messages and sends it to GitHub Models.
   d. Receives the LLM analysis.
   e. Posts the result back to the Slack channel via Slack API.

---

## Error Handling Strategy

- All errors posted back to the user as an ephemeral Slack message (visible only to them).
- GitHub Models API errors (rate limit, invalid model) produce a clear user-facing message.
- GitHub API errors (file not found, permission denied) produce actionable feedback (e.g., "Repository 'devops-infra' not found or token lacks access").
- Slack signature verification failure returns `401 Unauthorized` with no further processing.

---

## Future Considerations (out of scope for POC)

- Support for interactive Slack modals (e.g., confirmation before committing).
- Conversation memory / multi-turn interactions.
- Support for more command types beyond debug and file modification.
- Deployment automation (Dockerfile, Helm chart, CI/CD).
- Structured logging and observability.
