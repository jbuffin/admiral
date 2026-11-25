package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// MaxFileBytes is the largest file Admiral reads back from a share repo.
const MaxFileBytes = 64 << 10

// maxListing is the most entries GitHub's contents API lists for a directory; a full page may have hidden more.
const maxListing = 1000

// ErrListingFull means a directory listing hit GitHub's cap, so names may be missing from it.
var ErrListingFull = errors.New("the directory has 1000 or more entries, so GitHub's listing may be cut short")

// ErrTooLarge means a file is over MaxFileBytes.
var ErrTooLarge = errors.New("the file is over 64 KiB")

// MovedError means GitHub answered a repository name with a repository of another name.
type MovedError struct{ To string }

func (e *MovedError) Error() string { return "the repository moved to " + e.To }

// RepoInfo is what a share repo's settings say about whether Admiral may publish to it.
type RepoInfo struct {
	FullName string
	Public   bool
	Archived bool
	Pushable bool
}

// DirEntry is one file in a repo directory.
type DirEntry struct {
	Name string
	Blob string
}

// File is a file read from a repo.
type File struct {
	Blob  string
	Bytes []byte
}

// Login is the account gh is signed in as on the client's host.
func (c *Client) Login(ctx context.Context) (string, error) {
	out, err := c.rest(ctx, "GET", "user", nil)
	if err != nil {
		return "", err
	}
	var got struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(out, &got) != nil || got.Login == "" {
		return "", errors.New("GitHub's answer was not a user")
	}
	return got.Login, nil
}

// Repo reads a repository's visibility, archived flag and the account's push permission.
func (c *Client) Repo(ctx context.Context, owner, name string) (RepoInfo, error) {
	endpoint, err := repoEndpoint(owner, name)
	if err != nil {
		return RepoInfo{}, err
	}
	out, err := c.rest(ctx, "GET", endpoint, nil)
	if err != nil {
		return RepoInfo{}, err
	}
	var got struct {
		FullName    string `json:"full_name"`
		Private     bool   `json:"private"`
		Archived    bool   `json:"archived"`
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if json.Unmarshal(out, &got) != nil || got.FullName == "" {
		return RepoInfo{}, errors.New("GitHub's answer was not a repository")
	}
	// GitHub follows a rename or transfer, so a write to the old name would land in the new place.
	if !strings.EqualFold(got.FullName, owner+"/"+name) {
		return RepoInfo{}, &MovedError{To: got.FullName}
	}
	return RepoInfo{FullName: got.FullName, Public: !got.Private, Archived: got.Archived, Pushable: got.Permissions.Push}, nil
}

// CreateRepo makes a public repository under the signed-in account and returns its owner/name.
func (c *Client) CreateRepo(ctx context.Context, name, description string) (string, error) {
	if !ValidSegment(name) {
		return "", fmt.Errorf("%q is not a repository name", name)
	}
	out, err := c.rest(ctx, "POST", "user/repos", map[string]any{"name": name, "description": description, "private": false, "auto_init": false})
	if err != nil {
		return "", err
	}
	var got struct {
		FullName string `json:"full_name"`
	}
	if json.Unmarshal(out, &got) != nil || !ValidRepo(got.FullName) {
		return "", errors.New("GitHub's answer was not a repository")
	}
	return got.FullName, nil
}

// ListDir lists the files in a repo directory; a 404 error (HTTPStatus) means the directory is not there.
func (c *Client) ListDir(ctx context.Context, owner, repo, dir string) ([]DirEntry, error) {
	endpoint, err := contentsEndpoint(owner, repo, dir)
	if err != nil {
		return nil, err
	}
	out, err := c.rest(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	var got []struct {
		Name string `json:"name"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	if json.Unmarshal(out, &got) != nil {
		return nil, errors.New("GitHub's answer was not a directory listing")
	}
	if len(got) >= maxListing {
		return nil, ErrListingFull
	}
	files := make([]DirEntry, 0, len(got))
	for _, e := range got {
		if e.Type == "file" {
			files = append(files, DirEntry{Name: e.Name, Blob: e.SHA})
		}
	}
	return files, nil
}

// ReadFile reads one file; a file over MaxFileBytes is refused before its content is decoded.
func (c *Client) ReadFile(ctx context.Context, owner, repo, path string) (File, error) {
	endpoint, err := contentsEndpoint(owner, repo, path)
	if err != nil {
		return File{}, err
	}
	out, err := c.rest(ctx, "GET", endpoint, nil)
	if err != nil {
		return File{}, err
	}
	var got struct {
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if json.Unmarshal(out, &got) != nil || got.SHA == "" {
		return File{}, errors.New("GitHub's answer was not a file")
	}
	if got.Size > MaxFileBytes || len(got.Content) > MaxFileBytes*2 {
		return File{}, ErrTooLarge
	}
	if got.Encoding != "base64" {
		return File{}, errors.New("GitHub's answer was not base64 text")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(got.Content, "\n", ""))
	if err != nil {
		return File{}, errors.New("GitHub's file content was not base64")
	}
	if len(raw) > MaxFileBytes {
		return File{}, ErrTooLarge
	}
	return File{Blob: got.SHA, Bytes: raw}, nil
}

// PutFile creates the file, or with blob replaces that version of it, and returns the new commit's SHA ("" when GitHub gave none).
func (c *Client) PutFile(ctx context.Context, owner, repo, path, message string, content []byte, blob string) (string, error) {
	endpoint, err := contentsEndpoint(owner, repo, path)
	if err != nil {
		return "", err
	}
	body := map[string]string{"message": message, "content": base64.StdEncoding.EncodeToString(content)}
	if blob != "" {
		body["sha"] = blob
	}
	return c.commit(ctx, "PUT", endpoint, body)
}

// DeleteFile removes that version of the file and returns the new commit's SHA.
func (c *Client) DeleteFile(ctx context.Context, owner, repo, path, message, blob string) (string, error) {
	endpoint, err := contentsEndpoint(owner, repo, path)
	if err != nil {
		return "", err
	}
	return c.commit(ctx, "DELETE", endpoint, map[string]string{"message": message, "sha": blob})
}

func (c *Client) commit(ctx context.Context, method, endpoint string, body any) (string, error) {
	out, err := c.rest(ctx, method, endpoint, body)
	if err != nil {
		return "", err
	}
	var got struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	_ = json.Unmarshal(out, &got)
	if !commitSHA.MatchString(got.Commit.SHA) {
		return "", nil
	}
	return got.Commit.SHA, nil
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func repoEndpoint(owner, name string) (string, error) {
	if !ValidSegment(owner) || !ValidSegment(name) {
		return "", fmt.Errorf("%s/%s is not a repository", owner, name)
	}
	return "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

// contentsEndpoint is the contents API's path for a file or directory, each segment escaped.
func contentsEndpoint(owner, repo, path string) (string, error) {
	base, err := repoEndpoint(owner, repo)
	if err != nil {
		return "", err
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%q is not a repository path", path)
		}
		parts[i] = url.PathEscape(p)
	}
	return base + "/contents/" + strings.Join(parts, "/"), nil
}
