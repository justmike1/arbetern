package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/justmike1/ovad/commands"
	"github.com/justmike1/ovad/config"
	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/jira"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

//go:embed ui/*
var uiFS embed.FS

// ── Integration permission types & cache ────────────────────────────────────

type permission struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Granted     *bool  `json:"granted,omitempty"` // nil = unknown, true/false = checked
	Extra       bool   `json:"extra,omitempty"`   // true = scope exists on token but not needed by arbetern
}

type integration struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Configured  bool         `json:"configured"`
	AuthMode    string       `json:"auth_mode,omitempty"`
	ActiveModel string       `json:"active_model,omitempty"`
	Permissions []permission `json:"permissions"`
}

var (
	integrationsMu    sync.RWMutex
	integrationsCache []integration
)

func boolPtr(v bool) *bool { return &v }

// hasScope checks if a scope exists in a granted scopes list.
// For hierarchical scopes like "repo" covering "repo:status", does prefix matching.
func hasScope(granted []string, scope string) bool {
	for _, g := range granted {
		if g == scope {
			return true
		}
		if strings.HasPrefix(scope, g+":") || strings.HasPrefix(g, scope+":") {
			return true
		}
	}
	return false
}

// refreshIntegrations queries each configured integration's API for live
// permissions and stores the result in the in-memory cache.
func refreshIntegrations(
	cfg *config.Config,
	slackClient *ovadslack.Client,
	ghClient *github.Client,
	jiraClient *jira.Client,
	modelsClient *github.ModelsClient,
) {
	// --- Slack ---
	slackPerms := []permission{
		{Scope: "chat:write", Description: "Post messages and thread replies in channels", Required: true},
		{Scope: "channels:history", Description: "Read message history in public channels", Required: true},
		{Scope: "groups:history", Description: "Read message history in private channels", Required: true},
		{Scope: "im:history", Description: "Read message history in DMs", Required: false},
		{Scope: "mpim:history", Description: "Read message history in group DMs", Required: false},
		{Scope: "users:read", Description: "Read user profile information (name, email)", Required: true},
		{Scope: "commands", Description: "Register and receive slash commands", Required: true},
	}
	if cfg.SlackBotToken != "" {
		if scopes, err := slackClient.GetBotScopes(); err == nil && scopes != nil {
			known := make(map[string]bool, len(slackPerms))
			for i := range slackPerms {
				slackPerms[i].Granted = boolPtr(hasScope(scopes, slackPerms[i].Scope))
				known[slackPerms[i].Scope] = true
			}
			// Append extra scopes the token has that arbetern doesn't need.
			for _, s := range scopes {
				if !known[s] {
					slackPerms = append(slackPerms, permission{
						Scope:   s,
						Granted: boolPtr(true),
						Extra:   true,
					})
				}
			}
		}
	}

	// --- GitHub ---
	ghPerms := []permission{
		{Scope: "repo", Description: "Full access to private and public repositories (read, write, branches, PRs)", Required: true},
		{Scope: "read:user", Description: "Read authenticated user profile", Required: true},
		{Scope: "read:org", Description: "Read organization membership and list repos", Required: true},
		{Scope: "actions:read", Description: "Read workflow runs, jobs, and logs (CI/CD debugging)", Required: false},
		{Scope: "checks:read", Description: "Read check run annotations for detailed CI feedback", Required: false},
	}
	ghAuthMode := ""
	if cfg.GitHubToken != "" {
		ghAuthMode = "Personal Access Token"
		if ghClient != nil {
			if scopes, err := ghClient.GetGrantedScopes(context.Background()); err == nil && scopes != nil {
				known := make(map[string]bool, len(ghPerms))
				for i := range ghPerms {
					ghPerms[i].Granted = boolPtr(hasScope(scopes, ghPerms[i].Scope))
					known[ghPerms[i].Scope] = true
				}
				// Append extra scopes the token has that arbetern doesn't need.
				for _, s := range scopes {
					if !known[s] {
						ghPerms = append(ghPerms, permission{
							Scope:   s,
							Granted: boolPtr(true),
							Extra:   true,
						})
					}
				}
			}
		}
	}

	result := []integration{
		{
			ID:          "slack",
			Name:        "Slack",
			Configured:  cfg.SlackBotToken != "",
			AuthMode:    "Bot Token",
			Permissions: slackPerms,
		},
		{
			ID:          "github",
			Name:        "GitHub",
			Configured:  cfg.GitHubToken != "",
			AuthMode:    ghAuthMode,
			Permissions: ghPerms,
		},
	}

	// --- Jira ---
	if cfg.JiraConfigured() {
		authMode := "Basic Auth"
		if cfg.JiraUseOAuth() {
			authMode = "OAuth 2.0"
		}
		jiraPerms := []permission{
			{Scope: "BROWSE_PROJECTS", Description: "View projects, issues, and field metadata", Required: true},
			{Scope: "CREATE_ISSUES", Description: "Create new issues (tickets, stories, bugs)", Required: true},
			{Scope: "EDIT_ISSUES", Description: "Update issue descriptions, fields, and team assignments", Required: true},
			{Scope: "ASSIGN_ISSUES", Description: "Search assignable users and set issue assignees", Required: false},
			{Scope: "BROWSE_USERS", Description: "Search for Jira users by name or email (global permission)", Required: false},
		}
		if cfg.JiraUseOAuth() {
			jiraPerms = append(jiraPerms,
				permission{Scope: "read:jira-work", Description: "OAuth scope: read issues, projects, and boards", Required: true, Granted: boolPtr(true)},
				permission{Scope: "write:jira-work", Description: "OAuth scope: create and update issues", Required: true, Granted: boolPtr(true)},
				permission{Scope: "read:jira-user", Description: "OAuth scope: read user profiles for assignee resolution", Required: true, Granted: boolPtr(true)},
			)
		}

		keys := make([]string, 0, 5)
		for _, p := range jiraPerms {
			if p.Scope == strings.ToUpper(p.Scope) {
				keys = append(keys, p.Scope)
			}
		}
		if jiraClient != nil {
			if grants, err := jiraClient.GetMyPermissions(keys); err == nil {
				known := make(map[string]bool, len(jiraPerms))
				for i := range jiraPerms {
					if g, ok := grants[jiraPerms[i].Scope]; ok {
						jiraPerms[i].Granted = boolPtr(g)
					}
					known[jiraPerms[i].Scope] = true
				}
				// Append extra Jira permissions the user has that arbetern doesn't need.
				for scope, granted := range grants {
					if !known[scope] && granted {
						jiraPerms = append(jiraPerms, permission{
							Scope:   scope,
							Granted: boolPtr(true),
							Extra:   true,
						})
					}
				}
			}
		}

		result = append(result, integration{
			ID:          "jira",
			Name:        "Jira",
			Configured:  true,
			AuthMode:    authMode,
			Permissions: jiraPerms,
		})
	} else {
		result = append(result, integration{
			ID:         "jira",
			Name:       "Jira",
			Configured: false,
			Permissions: []permission{
				{Scope: "BROWSE_PROJECTS", Description: "View projects, issues, and field metadata", Required: true},
				{Scope: "CREATE_ISSUES", Description: "Create new issues (tickets, stories, bugs)", Required: true},
				{Scope: "EDIT_ISSUES", Description: "Update issue descriptions, fields, and team assignments", Required: true},
				{Scope: "ASSIGN_ISSUES", Description: "Search assignable users and set issue assignees", Required: false},
			},
		})
	}

	// --- Azure OpenAI ---
	if cfg.UseAzure() && modelsClient != nil {
		azurePerms := []permission{
			{Scope: "Cognitive Services OpenAI User", Description: "Azure RBAC role for chat completions inference", Required: true, Granted: boolPtr(true)},
		}

		activeModel := modelsClient.Model()

		// List all accessible models/deployments.
		if models, err := modelsClient.ListModels(context.Background()); err == nil {
			for _, m := range models {
				isActive := m == activeModel
				desc := "Available deployment"
				if isActive {
					desc = "Active deployment (currently in use)"
				}
				azurePerms = append(azurePerms, permission{
					Scope:       m,
					Description: desc,
					Required:    isActive,
					Granted:     boolPtr(true),
					Extra:       !isActive,
				})
			}
		}

		result = append(result, integration{
			ID:          "azure-openai",
			Name:        "Azure OpenAI",
			Configured:  true,
			AuthMode:    "API Key",
			ActiveModel: activeModel,
			Permissions: azurePerms,
		})
	}

	integrationsMu.Lock()
	integrationsCache = result
	integrationsMu.Unlock()
	log.Println("Integration permissions refreshed")
}

// startIntegrationsRefresher runs refreshIntegrations once immediately and
// then again every hour in a background goroutine.
func startIntegrationsRefresher(
	cfg *config.Config,
	slackClient *ovadslack.Client,
	ghClient *github.Client,
	jiraClient *jira.Client,
	modelsClient *github.ModelsClient,
) {
	refreshIntegrations(cfg, slackClient, ghClient, jiraClient, modelsClient)

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			refreshIntegrations(cfg, slackClient, ghClient, jiraClient, modelsClient)
		}
	}()
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	slackClient := ovadslack.NewClient(cfg.SlackBotToken)

	var ghClient *github.Client
	if cfg.GitHubToken != "" {
		ghClient = github.NewClient(cfg.GitHubToken)
	}

	var modelsClient *github.ModelsClient
	if cfg.UseAzure() {
		modelsClient = github.NewAzureModelsClient(cfg.AzureEndpoint, cfg.AzureAPIKey, cfg.GitHubModel)
		log.Printf("Using Azure OpenAI backend: %s (deployment: %s)", cfg.AzureEndpoint, cfg.GitHubModel)
	} else {
		modelsClient = github.NewModelsClient(cfg.GitHubToken, cfg.GitHubModel)
		log.Printf("Using GitHub Models backend (model: %s)", cfg.GitHubModel)
	}

	var jiraClient *jira.Client
	if cfg.JiraConfigured() {
		if cfg.JiraUseOAuth() {
			var err error
			jiraClient, err = jira.NewOAuthClient(cfg.JiraURL, cfg.JiraClientID, cfg.JiraClientSecret, cfg.JiraProject)
			if err != nil {
				log.Fatalf("Jira OAuth initialization failed: %v", err)
			}
			log.Printf("Jira integration enabled (OAuth): %s (default project: %s)", cfg.JiraURL, cfg.JiraProject)
		} else {
			jiraClient = jira.NewClient(cfg.JiraURL, cfg.JiraEmail, cfg.JiraAPIToken, cfg.JiraProject)
			log.Printf("Jira integration enabled (Basic Auth): %s (default project: %s)", cfg.JiraURL, cfg.JiraProject)
		}
	}

	// Discover agents and register per-agent webhook routes (/<agent>/webhook).
	agents, err := prompts.DiscoverAgents("")
	if err != nil {
		log.Fatalf("failed to discover agents: %v", err)
	}
	if len(agents) == 0 {
		log.Fatal("no agents found in agents/ directory")
	}

	// Start background integration permission refresher (runs once now, then every hour).
	startIntegrationsRefresher(cfg, slackClient, ghClient, jiraClient, modelsClient)

	for _, agent := range agents {
		ap, err := prompts.LoadAgent(agent.ID)
		if err != nil {
			log.Fatalf("failed to load prompts for agent %s: %v", agent.ID, err)
		}

		router := commands.NewRouter(slackClient, ghClient, modelsClient, jiraClient, ap, agent.ID, cfg.AppURL)
		handler := ovadslack.NewHandler(cfg.SlackSigningSecret, router.Handle)

		webhookPath := fmt.Sprintf("/%s/webhook", agent.ID)
		http.Handle(webhookPath, handler)
		log.Printf("Registered agent %q at %s", agent.ID, webhookPath)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Agent management UI (embedded static files) — behind IP whitelist if configured.
	uiContent, _ := fs.Sub(uiFS, "ui")
	uiCIDRs := parseCIDRs(cfg.UIAllowedCIDRs)
	if len(uiCIDRs) > 0 {
		log.Printf("UI IP whitelist enabled: %s", cfg.UIAllowedCIDRs)
	}
	uiHandler := ipWhitelist(uiCIDRs, http.StripPrefix("/ui/", http.FileServer(http.FS(uiContent))))
	http.Handle("/ui/", uiHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	// API: list agents with their prompts (read-only, discovered from agents/ directory).
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		agents, err := prompts.DiscoverAgents("")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to discover agents: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agents)
	})

	// API: UI settings.
	apiMux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		headerTitle := os.Getenv("UI_HEADER")
		if headerTitle == "" {
			headerTitle = "arbetern"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"header": headerTitle})
	})

	// API: integrations — serves cached integration permissions (refreshed hourly).
	apiMux.HandleFunc("/api/integrations", func(w http.ResponseWriter, r *http.Request) {
		integrationsMu.RLock()
		data := integrationsCache
		integrationsMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})

	http.Handle("/api/", ipWhitelist(uiCIDRs, apiMux))

	log.Printf("arbetern server starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
