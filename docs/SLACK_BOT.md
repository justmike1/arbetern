# Slack Bot Setup Guide

## Prerequisites

- A Slack workspace where you have admin permissions
- A public URL where the arbetern server will be deployed (e.g., `https://ai.example.com`). You will configure and deploy the server in Step 6 after obtaining the Slack credentials.

## Step 1: Create a Slack App

1. Go to https://api.slack.com/apps
2. Click **Create New App** > **From scratch**
3. Set the app name (e.g. `ovad` for the ovad agent)
4. Select your workspace
5. Click **Create App**

## Step 2: Configure Bot Token Scopes

1. In the left sidebar, go to **OAuth & Permissions**
2. Scroll to **Scopes** > **Bot Token Scopes**
3. Add the following scopes:

| Scope | Purpose |
|---|---|
| `commands` | Register and receive slash commands |
| `channels:history` | Read messages from public channels |
| `chat:write` | Post responses to channels |

## Step 3: Create the Slash Command

1. In the left sidebar, go to **Slash Commands**
2. Click **Create New Command**
3. Fill in:
   - **Command:** `/ovad` (or whatever you want the agent's slash command to be)
   - **Request URL:** `https://<your-server>/<agent-name>/webhook` (e.g. `https://ai.example.com/ovad/webhook`)
   - **Short Description:** `AI-powered DevOps assistant`
   - **Usage Hint:** `[debug latest message | add env var KEY=VALUE in file.tf in repo-name repository]`
4. Click **Save**

> **Multi-agent setup:** Create a separate Slack app (or slash command) for each agent, each pointing to its own `/<agent>/webhook` path.

## Step 4: Install the App to Your Workspace

1. In the left sidebar, go to **Install App**
2. Click **Install to Workspace**
3. Review the permissions and click **Allow**
4. Copy the **Bot User OAuth Token** (starts with `xoxb-`)

## Step 5: Get the Signing Secret

1. In the left sidebar, go to **Basic Information**
2. Under **App Credentials**, find **Signing Secret**
3. Click **Show** and copy the value

## Step 6: Configure Arbetern Environment Variables

Set these environment variables on the server running arbetern:

```
SLACK_BOT_TOKEN=xoxb-your-bot-token
SLACK_SIGNING_SECRET=your-signing-secret
GITHUB_TOKEN=your-github-pat
GITHUB_MODEL=openai/gpt-4o
PORT=8080
```

## Step 7: Invite the Bot to Channels

In each Slack channel where you want to use an agent, run:

```
/invite @<bot-name>
```

The bot needs to be present in a channel to read its message history.

## Usage

Once configured, use the agent's slash command in any channel the bot has been invited to:

```
/ovad please debug the latest message in this channel
```

```
/ovad please add env var KEY=NAME in amplify.tf in devops-infra repository
```

## Troubleshooting

**"dispatch_failed" error when using the command**
- The server is unreachable from Slack. Verify the Request URL is correct and the server is running.

**"not_in_channel" error in logs**
- Run `/invite @<bot-name>` in the channel.

**"invalid_auth" error in logs**
- The `SLACK_BOT_TOKEN` is wrong or expired. Reinstall the app and copy the new token.

**"channel_not_found" when reading history**
- For private channels, the bot needs the `groups:history` scope. Add it under **OAuth & Permissions** and reinstall the app.
