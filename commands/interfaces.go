package commands

import slacklib "github.com/slack-go/slack"

type SlackClient interface {
	FetchChannelHistory(channelID string, limit int) ([]slacklib.Message, error)
	FetchThreadReplies(channelID, threadTS string, limit int) ([]slacklib.Message, error)
	PostMessage(channelID, text string) (string, error)
	PostThreadReply(channelID, threadTS, text string) error
	GetPermalink(channelID, messageTS string) (string, error)
	GetUserInfo(userID string) (*slacklib.User, error)
}

// PromptProvider abstracts access to per-agent prompts.
type PromptProvider interface {
	Get(key string) string
	MustGet(key string) string
}
