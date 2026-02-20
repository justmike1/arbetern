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
	ghClient := github.NewClient(cfg.GitHubToken)
	modelsClient := github.NewModelsClient(cfg.GitHubToken, cfg.GitHubModel)

	router := commands.NewRouter(slackClient, ghClient, modelsClient)
	handler := ovadslack.NewHandler(cfg.SlackSigningSecret, router.Handle)

	http.Handle("/webhook", handler)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("ovad server starting on :%s using model %s", cfg.Port, cfg.GitHubModel)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
