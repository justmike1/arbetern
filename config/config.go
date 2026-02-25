package config

import (
	"fmt"
	"os"
)

const (
	defaultPort       = "8080"
	defaultModel      = "openai/gpt-4o"
	defaultAzureModel = "gpt-4o"
)

type Config struct {
	SlackBotToken      string
	SlackSigningSecret string
	GitHubToken        string
	GitHubModel        string
	AzureEndpoint      string
	AzureAPIKey        string
	Port               string
	UIAllowedCIDRs     string
	JiraURL            string
	JiraEmail          string
	JiraAPIToken       string
	JiraProject        string
	JiraClientID       string
	JiraClientSecret   string
	AppURL             string
}

// UseAzure returns true when Azure OpenAI credentials are configured.
func (c *Config) UseAzure() bool {
	return c.AzureEndpoint != "" && c.AzureAPIKey != ""
}

// JiraConfigured returns true when Jira credentials are present.
// Supports both Basic Auth (email + API token) and OAuth 2.0 (client ID + secret).
func (c *Config) JiraConfigured() bool {
	if c.JiraURL == "" {
		return false
	}
	return (c.JiraEmail != "" && c.JiraAPIToken != "") || (c.JiraClientID != "" && c.JiraClientSecret != "")
}

// JiraUseOAuth returns true when OAuth 2.0 client credentials are configured.
func (c *Config) JiraUseOAuth() bool {
	return c.JiraClientID != "" && c.JiraClientSecret != ""
}

func Load() (*Config, error) {
	cfg := &Config{
		SlackBotToken:      os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		GitHubToken:        os.Getenv("GITHUB_TOKEN"),
		GitHubModel:        os.Getenv("GITHUB_MODEL"),
		AzureEndpoint:      os.Getenv("AZURE_OPEN_AI_ENDPOINT"),
		AzureAPIKey:        os.Getenv("AZURE_API_KEY"),
		Port:               os.Getenv("PORT"),
		UIAllowedCIDRs:     os.Getenv("UI_ALLOWED_CIDRS"),
		JiraURL:            os.Getenv("JIRA_URL"),
		JiraEmail:          os.Getenv("JIRA_EMAIL"),
		JiraAPIToken:       os.Getenv("JIRA_API_TOKEN"),
		JiraProject:        os.Getenv("JIRA_PROJECT"),
		JiraClientID:       os.Getenv("JIRA_CLIENT_ID"),
		JiraClientSecret:   os.Getenv("JIRA_CLIENT_SECRET"),
		AppURL:             os.Getenv("APP_URL"),
	}

	if cfg.SlackBotToken == "" {
		return nil, fmt.Errorf("SLACK_BOT_TOKEN is required")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, fmt.Errorf("SLACK_SIGNING_SECRET is required")
	}

	// Either GitHub token or Azure credentials are required for LLM access.
	if cfg.GitHubToken == "" && !cfg.UseAzure() {
		return nil, fmt.Errorf("GITHUB_TOKEN is required (or set AZURE_OPEN_AI_ENDPOINT and AZURE_API_KEY)")
	}

	if cfg.GitHubModel == "" {
		if cfg.UseAzure() {
			cfg.GitHubModel = defaultAzureModel
		} else {
			cfg.GitHubModel = defaultModel
		}
	}
	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	return cfg, nil
}
