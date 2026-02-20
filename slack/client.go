package slack

import (
	"fmt"

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

func (c *Client) PostEphemeral(channelID, userID, text string) error {
	_, err := c.api.PostEphemeral(channelID, userID, slack.MsgOptionText(text, false))
	if err != nil {
		return fmt.Errorf("failed to post ephemeral message: %w", err)
	}
	return nil
}
