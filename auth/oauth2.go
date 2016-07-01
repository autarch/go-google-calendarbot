package auth

import (
	"encoding/json"
	"io/ioutil"

	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
)

type FileConfigProvider struct {
	file string
}

type FileTokenProvider struct {
	file string
}

func NewFileConfigProvider(file string) *FileConfigProvider {
	return &FileConfigProvider{
		file: file,
	}
}

func NewFileTokenProvider(file string) *FileTokenProvider {
	return &FileTokenProvider{
		file: file,
	}
}

func (p *FileConfigProvider) OAuth2Config(_ context.Context) (*oauth2.Config, error) {
	return ConfigFromFile(p.file)
}

func (p *FileTokenProvider) OAuth2Token(_ context.Context) (*oauth2.Token, error) {
	return TokenFromFile(p.file)
}

func ConfigFromFile(file string) (*oauth2.Config, error) {
	body, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read oauth config file")
	}

	config, err := google.ConfigFromJSON(body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get oauth config from file")
	}
	config.Scopes = []string{
		calendar.CalendarReadonlyScope,
		"https://www.googleapis.com/auth/userinfo.email",
	}
	return config, nil
}

func copyToken(token, stored *oauth2.Token) {
  token.AccessToken = stored.AccessToken
  token.RefreshToken = stored.RefreshToken
  token.Expiry = stored.Expiry
  token.TokenType = stored.TokenType
}

func TokenFromFile(file string) (*oauth2.Token, error) {
	var token oauth2.Token
	var stored oauth2.Token

	body, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read token file")
	}

	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal JSON")
	}

	copyToken(&token, &stored)
	return &token, nil
}
