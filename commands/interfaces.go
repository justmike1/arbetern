package commands

import slacklib "github.com/slack-go/slack"

type SlackClient interface {
	FetchChannelHistory(channelID string, limit int) ([]slacklib.Message, error)
	PostMessage(channelID, text string) error
}
