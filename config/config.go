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
}

// UseAzure returns true when Azure OpenAI credentials are configured.
func (c *Config) UseAzure() bool {
	return c.AzureEndpoint != "" && c.AzureAPIKey != ""
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
