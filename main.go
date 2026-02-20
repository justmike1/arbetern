package main

import (
	"log"
	"net/http"

	"github.com/justmike1/ovad/commands"
	"github.com/justmike1/ovad/config"
	"github.com/justmike1/ovad/github"
	ovadslack "github.com/justmike1/ovad/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	slackClient := ovadslack.NewClient(cfg.SlackBotToken)
	ghClient := github.NewClient(cfg.GitHubToken)
	modelsClient := github.NewModelsClient(cfg.GitHubToken, cfg.GitHubModel)

	router := commands.NewRouter(slackClient, ghClient, modelsClient)
	handler := ovadslack.NewHandler(cfg.SlackSigningSecret, router.Handle)

	http.Handle("/webhook", handler)

	log.Printf("ovad server starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
