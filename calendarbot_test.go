package calendarbot_test

import (
	"time"

	"github.com/lestrrat/google-calendarbot"
	"github.com/lestrrat/google-calendarbot/auth"
	"golang.org/x/net/context"
)

func ExampleCalendarBot() {
	ctx := context.Background()

	bot := calendarbot.New()
	bot.OAuth2Config = auth.NewFileConfigProvider("config.json")
	bot.OAuth2Token = auth.NewFileTokenProvider("token.json")
	bot.NotifyUpcomingEvents(ctx, time.Now(), 24*time.Hour)
}
