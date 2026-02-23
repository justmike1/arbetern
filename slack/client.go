package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/slack-go/slack"
)

type Client struct {
	api *slack.Client
}

func NewClient(botToken string) *Client {
	return &Client{api: slack.New(botToken)}
}

func (c *Client) FetchChannelHistory(channelID string, limit int) ([]slack.Message, error) {
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
	}

	resp, err := c.api.GetConversationHistory(params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel history: %w", err)
	}

	return resp.Messages, nil
}

func (c *Client) PostMessage(channelID, text string) error {
	_, _, err := c.api.PostMessage(channelID, slack.MsgOptionText(text, false))
	if err != nil {
		return fmt.Errorf("failed to post message: %w", err)
	}
	return nil
}

func (c *Client) PostThreadReply(channelID, threadTS, text string) error {
	_, _, err := c.api.PostMessage(channelID, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))
	if err != nil {
		return fmt.Errorf("failed to post thread reply: %w", err)
	}
	return nil
}

func (c *Client) PostEphemeral(channelID, userID, text string) error {
	_, err := c.api.PostEphemeral(channelID, userID, slack.MsgOptionText(text, false))
	if err != nil {
		return fmt.Errorf("failed to post ephemeral message: %w", err)
	}
	return nil
}

type webhookPayload struct {
	ResponseType string `json:"response_type"`
	Text         string `json:"text"`
}

func RespondToURL(responseURL, text string, ephemeral bool) error {
	respType := "in_channel"
	if ephemeral {
		respType = "ephemeral"
	}

	payload, err := json.Marshal(webhookPayload{
		ResponseType: respType,
		Text:         text,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal response payload: %w", err)
	}

	resp, err := http.Post(responseURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to post to response_url: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("response_url returned status %d", resp.StatusCode)
	}

	return nil
}
