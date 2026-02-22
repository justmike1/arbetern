package main

import (
	"log"
	"net/http"

	"github.com/justmike1/ovad/commands"
	"github.com/justmike1/ovad/config"
	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	if err := prompts.Load(""); err != nil {
		log.Fatalf("failed to load prompts: %v", err)
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

	router := commands.NewRouter(slackClient, ghClient, modelsClient)
	handler := ovadslack.NewHandler(cfg.SlackSigningSecret, router.Handle)

	http.Handle("/webhook", handler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("ovad server starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
