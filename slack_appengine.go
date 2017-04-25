// +build appengine

package calendarbot

import (
	"net/http"

	"github.com/lestrrat/slack"
	"golang.org/x/net/context"
	"google.golang.org/appengine/urlfetch"
)

func NewSlackClient(ctx context.Context, token string) *slack.Client {
	slackcl := slack.New(token)
	slackcl.HTTPClient = &http.Client{
		Transport: &urlfetch.Transport{
			Context: ctx,
		},
	}
	return slackcl
}
