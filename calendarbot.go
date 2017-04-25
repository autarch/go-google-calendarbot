package calendarbot

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"

	"github.com/lestrrat/slack"
	"github.com/pkg/errors"
)

type OAuth2ConfigProvider interface {
	OAuth2Config(context.Context) (*oauth2.Config, error)
}

type OAuth2TokenProvider interface {
	OAuth2Token(context.Context) (*oauth2.Token, error)
}

type EventCache interface {
	Add(context.Context, string, []byte, time.Duration) error
	Get(context.Context, string) (interface{}, error)
}

type cacheMissError struct{}

func (_ cacheMissError) IsCacheMiss() bool {
	return true
}
func (_ cacheMissError) Error() string {
	return "cache miss"
}

type cacheEntry struct {
	Value   []byte
	Expires time.Time
}

type memoryCache struct {
	data  map[string]cacheEntry
	mutex sync.Mutex
}

func newMemoryCache() *memoryCache {
	return &memoryCache{
		data: make(map[string]cacheEntry),
	}
}

func (c *memoryCache) Add(_ context.Context, key string, val []byte, expires time.Duration) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	e, ok := c.data[key]
	if ok {
		if e.Expires.Before(time.Now()) {
			delete(c.data, key)
		}
		return errors.New("entry exists")
	}
	c.data[key] = cacheEntry{
		Value:   val,
		Expires: time.Now().Add(expires),
	}
	return nil
}

func (c *memoryCache) Get(_ context.Context, key string) (interface{}, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	e, ok := c.data[key]
	if !ok {
		return nil, cacheMissError{}
	}

	if e.Expires.Before(time.Now()) {
		delete(c.data, key)
		return nil, cacheMissError{}
	}
	return e, nil
}

type Bot struct {
	Cache         EventCache
	CalendarName  string // "primary" by default
	Email         string // Identity
	OAuth2Config  OAuth2ConfigProvider
	OAuth2Token   OAuth2TokenProvider
	SlackChannel  string // Channel name to post
	SlackThumbURL string // Thumbnail URL to use when posting to Slack
	SlackToken    string // Access token for slack
	SlackUsername string // Username of the bot
}

func New() *Bot {
	return &Bot{
		Cache:        newMemoryCache(),
		CalendarName: `primary`,
	}
}

type cacheError interface {
	CacheMiss() bool
}

func IsCacheMiss(err error) bool {
	if cacheErr, ok := err.(cacheError); ok {
		return cacheErr.CacheMiss()
	}
	return false
}

func (b *Bot) NotifyIndividualEvents(ctx context.Context, t time.Time, delta time.Duration) error {
	s, err := b.CalendarService(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create calendar service")
	}

	// Collect events that are due in the given time frame
	start := t.Format(time.RFC3339)
	end := t.Add(delta).Format(time.RFC3339)

	events, err := s.Events.
		List(`primary`).
		TimeMin(start).
		TimeMax(end).
		SingleEvents(true).
		Do()
	if err != nil {
		return errors.Wrap(err, "failed to list events")
	}
	now := time.Now().UTC()
	for _, event := range events.Items {
		_, err := b.Cache.Get(ctx, event.Id)
		switch {
		case err == nil:
			// Found, go to next item
			// log.Debugf(ctx, "event %s has been processed in the last 15 minutes, skipping", event.Id)
			continue
		case IsCacheMiss(err):
			// Not found, need to process
		case err != nil:
			return errors.Wrap(err, "failed to communicate with cache")
		}

		t, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err != nil {
			return errors.Wrap(err, "failed to parse event start time")
		}
		diff := t.Sub(now)
		if diff < 0 { // event %s has negative offset. skipping
			b.Cache.Add(ctx, event.Id, []byte{0x1}, 15*time.Minute)
			continue
		}
		fields := []slack.AttachmentField{
			slack.AttachmentField{
				Title: "Start Time",
				Value: t.Format("15:04"),
			},
		}
		if txt := event.Description; txt != "" {
			fields = append(fields, slack.AttachmentField{
				Title: "Description",
				Value: txt,
			})
		}

		params := slack.NewPostMessageParameters()
		params.Username = b.SlackUsername
		params.Attachments = []slack.Attachment{
			slack.Attachment{
				Fallback:  event.Summary,
				Fields:    fields,
				ThumbURL:  b.SlackThumbURL,
				Title:     event.Summary,
				TitleLink: event.HtmlLink,
			},
		}
		txt := fmt.Sprintf("This event starts in %d minutes", int(diff.Minutes()))
		if err := postSlack(ctx, b.SlackToken, b.SlackChannel, txt, &params); err != nil {
			return errors.Wrap(err, "failed to post message to slack")
		}

		// Remember this job for the next 15 minutes so we don't do it again
		b.Cache.Add(ctx, event.Id, []byte{0x1}, 15*time.Minute)
	}
	return nil
}

// NotifyUpcomingEvents sends one message to slack
// containing all of the events that are scheduled to happen
// in the next `delta` amount of time, starting at `t`
func (b *Bot) NotifyUpcomingEvents(ctx context.Context, t time.Time, delta time.Duration) error {
	s, err := b.CalendarService(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create calendar service")
	}

	// Collect events that are due in the given time frame
	start := t.Format(time.RFC3339)
	end := t.Add(delta).Format(time.RFC3339)

	events, err := s.Events.
		List(b.CalendarName).
		TimeMin(start).
		TimeMax(end).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return errors.Wrap(err, "failed to list events")
	}

	// Nothing to do
	if len(events.Items) == 0 {
		return nil
	}

	// Create a message containing all events for the day
	buf := bytes.Buffer{}
	fields := make([]slack.AttachmentField, len(events.Items))
	for i, event := range events.Items {
		t1, err := time.Parse(time.RFC3339, event.Start.DateTime)
		if err != nil {
			return errors.Wrap(err, "failed to parse start date/time")
		}

		t2, err := time.Parse(time.RFC3339, event.End.DateTime)
		if err != nil {
			return errors.Wrap(err, "failed to parse end date/time")
		}

		buf.Reset()
		fmt.Fprintf(&buf, "%s-%s: <%s|%s>", t1.Format("15:04"), t2.Format("15:04"), event.HtmlLink, event.Summary)

		fields[i] = slack.AttachmentField{
			Value: buf.String(),
		}
	}

	buf.Reset()
	fmt.Fprintf(&buf, "Upcoming events between %s to %s", t.Format("2006 Jan 02 15:04"), t.Add(delta).Format("2006 Jan 02 15:04"))

	params := slack.NewPostMessageParameters()
	params.Username = b.SlackUsername
	params.Attachments = []slack.Attachment{
		slack.Attachment{
			Fallback: buf.String(),
			Fields:   fields,
			ThumbURL: b.SlackThumbURL,
			Title:    buf.String(),
		},
	}

	return errors.Wrap(postSlack(ctx, b.SlackToken, b.SlackChannel, "", &params), "failed to post message to slack")
}

func (b *Bot) CalendarService(ctx context.Context) (*calendar.Service, error) {
	token, err := b.OAuth2Token.OAuth2Token(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load OAuth2 token")
	}

	config, err := b.OAuth2Config.OAuth2Config(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to load OAuth2 config")
	}

	httpcl := config.Client(ctx, token)
	s, err := calendar.New(httpcl)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create google calendar service")
	}
	return s, nil
}

func channelID(slackcl *slack.Client, channelName string) (string, error) {
	channels, err := slackcl.GetChannels(false)
	if err != nil {
		return "", errors.Wrap(err, "failed to get channel list")
	}

	for _, ch := range channels {
		if ch.Name == channelName {
			return ch.ID, nil
		}
	}

	groups, err := slackcl.GetGroups(false)
	if err != nil {
		return "", errors.Wrap(err, "failed to get group list")
	}

	for _, g := range groups {
		if g.Name == channelName {
			return g.ID, nil
		}
	}
	return "", errors.New("failed to find matching channel/group")
}

func slackClient(ctx context.Context, token string) (*slack.Client, error) {
	slackcl := NewSlackClient(ctx, token)
	if _, err := slackcl.AuthTest(); err != nil {
		return nil, errors.Wrap(err, "slack authentication test failed")
	}
	return slackcl, nil
}

func postSlack(ctx context.Context, token, channel, txt string, params *slack.PostMessageParameters) error {
	slackcl, err := slackClient(ctx, token)
	if err != nil {
		return errors.Wrap(err, "failed to create and authenticate slack client")
	}

	chID, err := channelID(slackcl, channel)
	if err != nil {
		return errors.Wrap(err, "failed to find channel ID")
	}

	_, _, err = slackcl.PostMessage(chID, txt, *params)
	return errors.Wrap(err, "failed to post slack message")
}
