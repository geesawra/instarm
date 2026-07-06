package instapaper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ochronus/instapaper-go-client/instapaper"
)

type Bookmark struct {
	Error             string
	BookmarkID        int     `json:"bookmark_id,omitempty"`
	URL               string  `json:"url,omitempty"`
	Title             string  `json:"title,omitempty"`
	Description       string  `json:"description,omitempty"`
	Time              int     `json:"time,omitempty"`
	Starred           string  `json:"starred,omitempty"`
	PrivateSource     string  `json:"private_source,omitempty"`
	Hash              string  `json:"hash,omitempty"`
	Progress          float64 `json:"progress,omitempty"`
	ProgressTimestamp int     `json:"progress_timestamp,omitempty"`
	Tags              []struct {
		ID    int     `json:"id"`
		Name  string  `json:"name"`
		Slug  string  `json:"slug"`
		Time  float64 `json:"time"`
		Count int     `json:"count"`
		Hash  string  `json:"hash"`
	} `json:"tags,omitempty"`
}

func (b *Bookmark) UnmarshalJSON(data []byte) error {
	type Tb Bookmark
	var tb Tb

	if err := json.Unmarshal(data, &tb); err != nil {
		return fmt.Errorf("unmarshal response into bookmark: %w", err)
	}

	*b = Bookmark(tb)

	return nil
}

type Bookmarks []Bookmark

type Instapaper struct {
	client instapaper.Client
}

func New(key, secret, username, password string) (*Instapaper, error) {
	ic, err := instapaper.NewClient(key, secret, username, password)
	if err != nil {
		return nil, fmt.Errorf("create instapaper client: %w", err)
	}

	if err := ic.Authenticate(); err != nil {
		return nil, fmt.Errorf("authenticate instapaper client: %w", err)
	}

	ic.BaseURL = "https://www.instapaper.com/api/1"

	return &Instapaper{
		client: ic,
	}, nil
}

func (i Instapaper) Unread(tag string) (Bookmarks, error) {
	listParams := url.Values{}
	listParams.Set("folder_id", "unread")
	listParams.Set("limit", "500")

	resp, err := i.client.Call("/bookmarks/list", listParams)
	if err != nil {
		return nil, fmt.Errorf("list unread articles: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp.Body)
	}

	bookmarks, err := decodeBookmarks(resp.Body)
	if err != nil {
		return nil, err
	}

	return filterByTag(bookmarks, tag), nil
}

func decodeBookmarks(r io.Reader) (Bookmarks, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	// The documented format is a JSON object with a "bookmarks" array.
	var obj struct {
		Bookmarks Bookmarks `json:"bookmarks"`
	}
	if err := json.Unmarshal(body, &obj); err == nil {
		if obj.Bookmarks == nil {
			return Bookmarks{}, nil
		}
		return obj.Bookmarks, nil
	}

	// Instapaper error responses are JSON arrays of error objects.
	var errors []struct {
		ErrorCode int    `json:"error_code"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &errors); err == nil && len(errors) > 0 && errors[0].ErrorCode != 0 {
		return nil, fmt.Errorf("instapaper API error %d: %s", errors[0].ErrorCode, errors[0].Message)
	}

	// The legacy format is a JSON array of mixed objects.
	var arr []Bookmark
	if err := json.Unmarshal(body, &arr); err == nil {
		filtered := make(Bookmarks, 0, len(arr))
		for _, b := range arr {
			if b.BookmarkID != 0 {
				filtered = append(filtered, b)
			}
		}
		return filtered, nil
	}

	return nil, fmt.Errorf("unmarshal bookmarks: %w", err)
}

func filterByTag(bookmarks Bookmarks, tag string) Bookmarks {
	if tag == "" {
		return bookmarks
	}

	filtered := make(Bookmarks, 0)
	for _, b := range bookmarks {
		for _, t := range b.Tags {
			if t.Name == tag {
				filtered = append(filtered, b)
				break
			}
		}
	}
	return filtered
}

func (i Instapaper) HTMLContent(id int) (string, error) {
	contentParams := url.Values{}
	contentParams.Add("bookmark_id", strconv.Itoa(id))

	resp, err := i.client.Call("/bookmarks/get_text", contentParams)
	if err != nil {
		return "", fmt.Errorf("get bookmark content: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", readError(resp.Body)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read html content from request response: %w", err)
	}

	return string(content), nil
}

func (i Instapaper) Archive(id int) error {
	params := url.Values{}
	params.Set("bookmark_id", strconv.Itoa(id))

	resp, err := i.client.Call("/bookmarks/archive", params)
	if err != nil {
		return fmt.Errorf("archive bookmark: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return readError(resp.Body)
	}

	return nil
}

// MarkAsRead updates the bookmark's read progress to 100% and archives it.
func (i Instapaper) MarkAsRead(id int) error {
	params := url.Values{}
	params.Set("bookmark_id", strconv.Itoa(id))
	params.Set("progress", "1")
	params.Set("progress_timestamp", strconv.FormatInt(time.Now().Unix(), 10))

	resp, err := i.client.Call("/bookmarks/update_read_progress", params)
	if err != nil {
		return fmt.Errorf("update read progress: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return readError(resp.Body)
	}

	return i.Archive(id)
}

func readError(r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read error response body: %w", err)
	}

	// Single error object: {"error": "..."}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return fmt.Errorf("api error: %s", e.Error)
	}

	// Array of error objects: [{"error_code": 1240, "message": "..."}]
	var errors []struct {
		ErrorCode int    `json:"error_code"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &errors); err == nil && len(errors) > 0 && errors[0].ErrorCode != 0 {
		return fmt.Errorf("instapaper API error %d: %s", errors[0].ErrorCode, errors[0].Message)
	}

	return fmt.Errorf("api error: %s", string(body))
}
