// +build !appengine

package calendarbot

import (
	"context"

	"github.com/lestrrat/slack"
)

func NewSlackClient(ctx context.Context, token string) *slack.Client {
	return slack.New(token)
}
