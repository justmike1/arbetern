# arbetern

*Yiddish for "workers."*

An orchestration platform for AI agents in the enterprise. Each agent lives in its own directory under `agents/`, with dedicated prompts and a defined professional scope. Arbetern provides the runtime, routing, UI, and integrations — agents bring the expertise.

## Current Agents

| Agent | Profession | Description |
|---|---|---|
| **ovad** | DevOps & SRE Engineer | Debugs CI/CD failures, reads/modifies repo files, opens PRs — all from a Slack slash command |

## Quick Start

### Prerequisites

- Go 1.25+
- A Slack app with a slash command pointing to `/<agent>/webhook` (see [docs/SLACK_BOT.md](docs/SLACK_BOT.md))
- A GitHub PAT with repo access (see [docs/GITHUB_PAT.md](docs/GITHUB_PAT.md))
- (Optional) Azure OpenAI credentials for LLM inference

### Environment Variables

| Variable | Required | Description |
|---|---|---|
| `SLACK_BOT_TOKEN` | yes | Slack bot OAuth token (`xoxb-...`) |
| `SLACK_SIGNING_SECRET` | yes | Slack app signing secret |
| `GITHUB_TOKEN` | yes* | GitHub PAT (*or* use Azure OpenAI) |
| `GITHUB_MODEL` | no | Model ID (default: `openai/gpt-4o`) |
| `AZURE_OPEN_AI_ENDPOINT` | no | Azure OpenAI endpoint URL |
| `AZURE_API_KEY` | no | Azure OpenAI API key |
| `PORT` | no | HTTP port (default: `8080`) |
| `PROMPTS_FILE` | no | Path to prompts YAML (default: per-agent `agents/<name>/prompts.yaml`) |
| `AGENTS_DIR` | no | Path to agents directory (default: `agents`) |
| `UI_HEADER` | no | Custom header text for the web UI (default: `arbetern`) |

### Run Locally

```bash
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_SIGNING_SECRET=...
export GITHUB_TOKEN=ghp_...
go run .
```

### Docker

```bash
docker build -t arbetern .
docker run -e SLACK_BOT_TOKEN -e SLACK_SIGNING_SECRET -e GITHUB_TOKEN arbetern
```

### Helm

```bash
helm upgrade --install arbetern ./helm -f deploy.local.values.yaml
```

## Web UI

Visit `/ui/` to see all registered agents. Click an agent card to view its prompts (read-only). The UI auto-discovers agents from the `agents/` directory.

- Drop a `logo.png` into `ui/` to replace the default icon
- Set `UI_HEADER` env var to customize the navbar title

## Adding a New Agent

1. Create a directory under `agents/`:
   ```
   agents/my-agent/prompts.yaml
   ```
2. Define prompts in the YAML file (keys like `security`, `classifier`, `general`, `debug`, etc.)
3. Rebuild and deploy — the agent will appear in the UI and get a webhook at `/<agent-name>/webhook`
4. Create a Slack slash command pointing to `https://<your-host>/<agent-name>/webhook`

> **Note:** Each agent directory under `agents/` is automatically discovered at startup and registered with its own webhook route (`/<agent>/webhook`). Create a Slack slash command per agent pointing to the corresponding path.

## Project Structure

```
main.go              # entrypoint, HTTP server, API
agents/              # agent definitions (one directory per agent)
  ovad/
    prompts.yaml     # ovad system prompts
config/              # env var loading
commands/            # intent routing, debug/general handlers
github/              # GitHub API client + Models/Azure API client
slack/               # Slack webhook handler + response helpers
prompts/             # YAML prompt loader + agent discovery
ui/                  # embedded web UI (agent manager)
helm/                # Helm chart
docs/                # setup guides (Slack, GitHub PAT)
```

## Customizing Prompts

Edit `agents/ovad/prompts.yaml` to change LLM behavior without recompiling. Keys: `intro`, `security`, `classifier`, `debug`, `general`.
