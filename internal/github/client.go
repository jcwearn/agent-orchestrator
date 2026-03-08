package github

import (
	"context"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gogithub "github.com/google/go-github/v84/github"
)

// Client wraps the GitHub API client with App authentication.
type Client struct {
	*gogithub.Client
	AppID          int64
	InstallationID int64
	transport      *ghinstallation.Transport
}

// NewClient creates a GitHub client authenticated as a GitHub App installation.
func NewClient(appID, installationID int64, privateKey []byte) (*Client, error) {
	transport, err := ghinstallation.New(http.DefaultTransport, appID, installationID, privateKey)
	if err != nil {
		return nil, err
	}

	gc := gogithub.NewClient(&http.Client{Transport: transport})

	return &Client{
		Client:         gc,
		AppID:          appID,
		InstallationID: installationID,
		transport:      transport,
	}, nil
}

// Token returns a fresh GitHub App installation token for git operations.
// Installation tokens are valid for 1 hour.
func (c *Client) Token() (string, error) {
	return c.transport.Token(context.Background())
}
