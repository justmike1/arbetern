# Jira Integration

Arbetern can create Jira tickets directly from Slack conversations — for example, turning a generated test plan into a trackable task.

## Required Credentials

| Environment Variable | Description |
|---|---|
| `JIRA_URL` | Your Atlassian instance URL (e.g. `https://yourorg.atlassian.net`) |
| `JIRA_EMAIL` | The email address of the Atlassian account used for authentication |
| `JIRA_API_TOKEN` | A Jira API token (not your account password) |
| `JIRA_PROJECT` | *(optional)* Default project key (e.g. `ENG`). If omitted, the bot will ask which project to use. |

## Step-by-step Setup

### 1. Get Your Atlassian Instance URL

This is the base URL you use to access Jira, e.g. `https://yourorg.atlassian.net`.

### 2. Create a Jira API Token

Arbetern uses **Basic Auth** (email + token), which requires a **classic API token**.

> **Important:** Do NOT use "Create API token with scopes" — scoped tokens are designed for Atlassian Forge/Connect apps and do not work with Basic Auth REST API calls. They will fail with HTTP 401.

1. Log in to [https://id.atlassian.com/manage-profile/security/api-tokens](https://id.atlassian.com/manage-profile/security/api-tokens).
2. Click **Create API token** (the button *without* scopes).
3. Give it a label (e.g. `Arbetern`) and click **Create**.
4. Copy the token — it is only shown once.

Classic API tokens inherit all permissions of the account that created them, so the account must have permission to create issues in the target Jira project.

> **Tip:** Do NOT use a personal admin account — the token would inherit full admin access. Create a dedicated service account instead (see below).

> **Tip:** Use a dedicated service account rather than a personal account, so the bot's access isn't tied to a single person.

### 3. Note the Account Email

Use the email address associated with the Atlassian account that created the API token. This is the value for `JIRA_EMAIL`.

### 4. Find Your Project Key

Open Jira and navigate to the target project. The project key is the prefix shown on issue IDs (e.g. if issues are `ENG-123`, the key is `ENG`).

You can also let the bot discover projects at runtime — users can ask it to `list jira projects`.

## Helm Deployment

Add the credentials to your values override file:

```yaml
secretValues:
  jira-url: "https://yourorg.atlassian.net"
  jira-email: "bot@yourorg.com"
  jira-api-token: "ATATT3x..."
  jira-project: "ENG"
```

Or create the secret manually:

```bash
kubectl create secret generic arbetern-secrets \
  --from-literal=jira-url=https://yourorg.atlassian.net \
  --from-literal=jira-email=bot@yourorg.com \
  --from-literal=jira-api-token=ATATT3x... \
  --from-literal=jira-project=ENG
```

## Required Jira Permissions

Classic API tokens inherit **all** permissions of the account, so using a personal admin account is dangerous. Create a dedicated service account with minimal access.

### Setting Up a Service Account

1. **Create a new Atlassian account** for the bot (e.g. `arbetern-bot@yourorg.com`).
   - Use a shared mailbox or group alias so it's not tied to one person.
   - Do **not** add this account to any admin groups (`jira-administrators`, `site-admins`, etc.).

2. **Grant project-level access** — in each Jira project the bot should work with:
   - Go to **Project Settings → People**.
   - Add the service account with the **Member** role.
   - This grants the minimum permissions needed:

   | Permission Key | Name | Why |
   |---|---|---|
   | `BROWSE_PROJECTS` | Browse Projects | List projects and view project metadata |
   | `CREATE_ISSUES` | Create Issues | Create new tickets via the API |

3. **Generate the API token** from the service account:
   - Log in as the service account at [id.atlassian.com](https://id.atlassian.com/manage-profile/security/api-tokens).
   - Create a classic API token (without scopes).
   - Use this token as `JIRA_API_TOKEN` and the service account email as `JIRA_EMAIL`.

> **Note:** If your Jira instance uses a custom permission scheme, verify that the *Member* role includes `BROWSE_PROJECTS` and `CREATE_ISSUES`. Check under **Jira Administration → Permission Schemes**.

## Usage

Once configured, the bot exposes two tools to the LLM:

- **create_jira_ticket** — create an issue with summary, description, type, and labels
- **list_jira_projects** — discover available project keys

Example Slack commands:

```
/arbetern create a Jira ticket from the test plan above
/arbetern open a bug in project QA for the login timeout issue
```
