# ovad

Slack bot that integrates with GitHub Models API. Analyses channel messages, modifies files via PRs, and answers general DevOps questions using an LLM agent with tool calling.

## Commands

```
/ovad debug the latest messages              # analyze recent channel alerts
/ovad add env var DB=prod in main.tf in repo  # modify a file and open a PR
/ovad list all repositories                   # general query (LLM decides how)
```

## Setup

### Prerequisites

- Go 1.25+
- A Slack app with a slash command pointing to `/webhook` (see [docs/SLACK_BOT.md](docs/SLACK_BOT.md))
- A GitHub PAT with repo access
- (Optional) GitHub Models API access for non-default models

### Environment Variables

| Variable | Required | Description |
|---|---|---|
| `SLACK_BOT_TOKEN` | yes | Slack bot OAuth token (`xoxb-...`) |
| `SLACK_SIGNING_SECRET` | yes | Slack app signing secret |
| `GITHUB_TOKEN` | yes | GitHub PAT |
| `GITHUB_MODEL` | no | Model ID (default: `openai/gpt-4o`) |
| `PORT` | no | HTTP port (default: `8080`) |
| `PROMPTS_FILE` | no | Path to prompts YAML (default: `prompts.yaml`) |

### Run Locally

```bash
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_SIGNING_SECRET=...
export GITHUB_TOKEN=ghp_...
go run .
```

### Docker

```bash
docker build -t ovad .
docker run -e SLACK_BOT_TOKEN -e SLACK_SIGNING_SECRET -e GITHUB_TOKEN ovad
```

### Helm

```bash
helm upgrade --install ovad ./helm -f deploy.local.values.yaml
```

## Project Structure

```
main.go           # entrypoint, HTTP server
prompts.yaml      # externalized system prompts
config/           # env var loading
commands/         # intent routing, debug/filemod/general handlers
github/           # GitHub API client + Models API client
slack/            # Slack webhook handler + response helpers
prompts/          # YAML prompt loader
helm/             # Helm chart
docs/             # Slack bot setup guide
```

## Customizing Prompts

Edit `prompts.yaml` to change LLM behavior without recompiling. Keys: `classifier`, `debug`, `filemod`, `filemod_parser`, `general`.
