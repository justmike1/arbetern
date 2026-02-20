package config

import (
	"fmt"
	"os"
)

const (
	defaultPort  = "8080"
	defaultModel = "openai/gpt-4o"
)

type Config struct {
	SlackBotToken      string
	SlackSigningSecret string
	GitHubToken        string
	GitHubModel        string
	Port               string
}

func Load() (*Config, error) {
	cfg := &Config{
		SlackBotToken:      os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		GitHubToken:        os.Getenv("GITHUB_TOKEN"),
		GitHubModel:        os.Getenv("GITHUB_MODEL"),
		Port:               os.Getenv("PORT"),
	}

	if cfg.SlackBotToken == "" {
		return nil, fmt.Errorf("SLACK_BOT_TOKEN is required")
	}
	if cfg.SlackSigningSecret == "" {
		return nil, fmt.Errorf("SLACK_SIGNING_SECRET is required")
	}
	if cfg.GitHubToken == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN is required")
	}
	if cfg.GitHubModel == "" {
		cfg.GitHubModel = defaultModel
	}
	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	return cfg, nil
}
