package main

import (
	"embed"
	"encoding/json"
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

	// API: list agents with their prompts (read-only).
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		type agent struct {
			ID         string            `json:"id"`
			Name       string            `json:"name"`
			Profession string            `json:"profession"`
			Prompts    map[string]string `json:"prompts"`
		}

		allPrompts := prompts.GetAll()
		agents := []agent{
			{
				ID:         "ovad",
				Name:       "ovad",
				Profession: "DevOps & SRE Engineer",
				Prompts:    allPrompts,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(agents)
	})

	// API: UI settings.
	http.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		headerTitle := os.Getenv("UI_HEADER")
		if headerTitle == "" {
			headerTitle = "ovad"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"header": headerTitle})
	})

	log.Printf("ovad server starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
