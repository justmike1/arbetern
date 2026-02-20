package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const modelsAPIURL = "https://models.github.ai/inference/chat/completions"

type ModelsClient struct {
	token      string
	model      string
	httpClient *http.Client
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []Tool        `json:"tools,omitempty"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewModelsClient(token, model string) *ModelsClient {
	return &ModelsClient{
		token:      token,
		model:      model,
		httpClient: &http.Client{},
	}
}

func (m *ModelsClient) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	resp, err := m.doChat(ctx, messages, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("GitHub Models returned no choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func (m *ModelsClient) CompleteWithTools(ctx context.Context, messages []ChatMessage, tools []Tool) (*ChatResponse, error) {
	return m.doChat(ctx, messages, tools)
}

func (m *ModelsClient) doChat(ctx context.Context, messages []ChatMessage, tools []Tool) (*ChatResponse, error) {
	reqBody := chatRequest{
		Model:    m.model,
		Messages: messages,
		Tools:    tools,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, modelsAPIURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.token)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to GitHub Models failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub Models API returned %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("GitHub Models error: %s", chatResp.Error.Message)
	}

	return &chatResp, nil
}

func NewChatMessage(role, content string) ChatMessage {
	return ChatMessage{Role: role, Content: content}
}

func NewToolResultMessage(toolCallID, content string) ChatMessage {
	return ChatMessage{Role: "tool", Content: content, ToolCallID: toolCallID}
}
