package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"github.com/justmike1/ovad/commands"
	"github.com/justmike1/ovad/config"
	"github.com/justmike1/ovad/github"
	"github.com/justmike1/ovad/prompts"
	ovadslack "github.com/justmike1/ovad/slack"
)

//go:embed ui/*
var uiFS embed.FS

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

	// Discover agents and register per-agent webhook routes (/<agent>/webhook).
	agents, err := prompts.DiscoverAgents("")
	if err != nil {
		log.Fatalf("failed to discover agents: %v", err)
	}
	if len(agents) == 0 {
		log.Fatal("no agents found in agents/ directory")
	}

	for _, agent := range agents {
		ap, err := prompts.LoadAgent(agent.ID)
		if err != nil {
			log.Fatalf("failed to load prompts for agent %s: %v", agent.ID, err)
		}

		router := commands.NewRouter(slackClient, ghClient, modelsClient, ap, agent.ID)
		handler := ovadslack.NewHandler(cfg.SlackSigningSecret, router.Handle)

		webhookPath := fmt.Sprintf("/%s/webhook", agent.ID)
		http.Handle(webhookPath, handler)
		log.Printf("Registered agent %q at %s", agent.ID, webhookPath)
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Agent management UI (embedded static files).
	uiContent, _ := fs.Sub(uiFS, "ui")
	http.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(uiContent))))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	// API: list agents with their prompts (read-only, discovered from agents/ directory).
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		agents, err := prompts.DiscoverAgents("")
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to discover agents: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agents)
	})

	// API: UI settings.
	http.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		headerTitle := os.Getenv("UI_HEADER")
		if headerTitle == "" {
			headerTitle = "arbetern"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"header": headerTitle})
	})

	log.Printf("arbetern server starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
