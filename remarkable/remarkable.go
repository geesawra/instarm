// Package remarkable wraps the ddvk/rmapi library so instarm can upload EPUBs
// to a reMarkable cloud account using tokens provided via environment
// variables, without relying on rmapi's YAML config file.
package remarkable

import (
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/juruen/rmapi/api"
	"github.com/juruen/rmapi/config"
	"github.com/juruen/rmapi/filetree"
	"github.com/juruen/rmapi/model"
	"github.com/juruen/rmapi/transport"
)

// Config holds the tokens needed to authenticate with the reMarkable cloud.
type Config struct {
	DeviceToken string
	UserToken   string
}

// Client is a thin wrapper around rmapi's ApiCtx.
type Client struct {
	ctx         api.ApiCtx
	httpCtx     *transport.HttpClientCtx
	deviceToken string
	syncVersion api.SyncVersion
}

// Authenticate exchanges a one-time code from
// https://my.remarkable.com/device/browser/connect for a device token and
// user token pair.
func Authenticate(code string) (Config, error) {
	if len(code) != 8 {
		return Config{}, fmt.Errorf("one-time code must be 8 characters, got %d", len(code))
	}

	httpCtx := transport.CreateHttpClientCtx(model.AuthTokens{})

	deviceToken, err := requestDeviceToken(&httpCtx, code)
	if err != nil {
		return Config{}, fmt.Errorf("request device token: %w", err)
	}

	httpCtx.Tokens.DeviceToken = deviceToken

	userToken, err := refreshUserToken(&httpCtx)
	if err != nil {
		return Config{}, fmt.Errorf("request user token: %w", err)
	}

	return Config{
		DeviceToken: deviceToken,
		UserToken:   userToken,
	}, nil
}

// New creates a Client from the supplied tokens. If the user token is expired,
// it attempts to refresh it using the device token.
func New(cfg Config) (*Client, error) {
	if cfg.DeviceToken == "" {
		return nil, fmt.Errorf("device token is required")
	}

	tokens := model.AuthTokens{
		DeviceToken: cfg.DeviceToken,
		UserToken:   cfg.UserToken,
	}
	httpCtx := transport.CreateHttpClientCtx(tokens)

	userInfo, err := api.ParseToken(tokens.UserToken)
	if err != nil {
		newUserToken, refreshErr := refreshUserToken(&httpCtx)
		if refreshErr != nil {
			return nil, fmt.Errorf("user token invalid and refresh failed: %w (refresh error: %v)", err, refreshErr)
		}
		httpCtx.Tokens.UserToken = newUserToken
		userInfo, err = api.ParseToken(newUserToken)
		if err != nil {
			return nil, fmt.Errorf("parse refreshed user token: %w", err)
		}
	}

	ctx, err := api.CreateApiCtx(&httpCtx, userInfo.SyncVersion)
	if err != nil {
		return nil, fmt.Errorf("create rmapi context: %w", err)
	}

	return &Client{
		ctx:         ctx,
		httpCtx:     &httpCtx,
		deviceToken: cfg.DeviceToken,
		syncVersion: userInfo.SyncVersion,
	}, nil
}

// refresh exchanges the stored device token for a new user token and updates
// the HTTP context used by subsequent API calls.
func (c *Client) refresh() error {
	if c.deviceToken == "" {
		return fmt.Errorf("device token is missing")
	}
	newUserToken, err := refreshUserToken(c.httpCtx)
	if err != nil {
		return fmt.Errorf("refresh user token: %w", err)
	}
	c.httpCtx.Tokens.UserToken = newUserToken
	return nil
}

// Folder represents a reMarkable directory.
type Folder struct {
	ID   string
	Name string
	Path string
}

// ListFolders returns all directories in the reMarkable account with their
// IDs and full paths. Useful for finding the right REMARKABLE_FOLDER_ID.
func (c *Client) ListFolders() []Folder {
	root := c.ctx.Filetree().Root()
	var folders []Folder

	var walk func(node *model.Node, path string)
	walk = func(node *model.Node, path string) {
		if node.IsDirectory() && !node.IsRoot() && node.Id() != filetree.TrashID {
			folders = append(folders, Folder{
				ID:   node.Id(),
				Name: node.Name(),
				Path: path + "/" + node.Name(),
			})
		}
		for _, child := range node.Children {
			if child.IsDirectory() {
				walk(child, path+"/"+node.Name())
			}
		}
	}

	for _, child := range root.Children {
		if child.IsDirectory() && child.Id() != filetree.TrashID {
			walk(child, "")
		}
	}

	sort.Slice(folders, func(i, j int) bool {
		return folders[i].Path < folders[j].Path
	})

	return folders
}

// ResolveFolderID turns a reMarkable folder ID or path into a folder ID
// suitable for UploadDocument. An empty input resolves to the root (empty ID).
func (c *Client) ResolveFolderID(input string) (string, error) {
	if input == "" {
		return "", nil
	}

	// Try interpreting the input as a document/folder ID first.
	if node := c.ctx.Filetree().NodeById(input); node != nil {
		if node.IsFile() {
			return "", fmt.Errorf("%q is a file, not a folder", input)
		}
		return input, nil
	}

	// Otherwise treat it as a path like "My Folder/Subfolder".
	node, err := c.ctx.Filetree().NodeByPath(input, c.ctx.Filetree().Root())
	if err != nil {
		return "", fmt.Errorf("folder %q not found: %w", input, err)
	}
	if node.IsFile() {
		return "", fmt.Errorf("path %q is a file, not a folder", input)
	}
	return node.Id(), nil
}

// UploadDocument uploads the file at path to the reMarkable folder identified
// by folderID. An empty folderID uploads to the root. If the upload fails
// because the user token expired, the token is refreshed and the upload is
// retried once.
func (c *Client) UploadDocument(path, folderID string) error {
	if _, err := c.ctx.UploadDocument(folderID, path, true, nil, nil, nil, nil); err != nil {
		if !errors.Is(err, transport.ErrUnauthorized) {
			return fmt.Errorf("upload %q: %w", path, err)
		}
		if refreshErr := c.refresh(); refreshErr != nil {
			return fmt.Errorf("upload %q: 401 unauthorized and token refresh failed: %w", path, refreshErr)
		}
		if _, err := c.ctx.UploadDocument(folderID, path, true, nil, nil, nil, nil); err != nil {
			return fmt.Errorf("upload %q after token refresh: %w", path, err)
		}
	}
	return nil
}

func requestDeviceToken(http *transport.HttpClientCtx, code string) (string, error) {
	req := model.DeviceTokenRequest{
		Code:       code,
		DeviceDesc: "desktop-linux",
		DeviceId:   uuid.New().String(),
	}
	resp := transport.BodyString{}
	if err := http.Post(transport.EmptyBearer, config.NewTokenDevice, req, &resp); err != nil {
		return "", err
	}
	return resp.Content, nil
}

func refreshUserToken(http *transport.HttpClientCtx) (string, error) {
	resp := transport.BodyString{}
	if err := http.Post(transport.DeviceBearer, config.NewUserDevice, nil, &resp); err != nil {
		return "", err
	}
	return resp.Content, nil
}
