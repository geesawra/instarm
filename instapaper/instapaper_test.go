package instapaper

import (
	"reflect"
	"strings"
	"testing"
)

func tag(name string) []struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Slug  string  `json:"slug"`
	Time  float64 `json:"time"`
	Count int     `json:"count"`
	Hash  string  `json:"hash"`
} {
	return []struct {
		ID    int     `json:"id"`
		Name  string  `json:"name"`
		Slug  string  `json:"slug"`
		Time  float64 `json:"time"`
		Count int     `json:"count"`
		Hash  string  `json:"hash"`
	}{{
		Name: name,
		Slug: name,
	}}
}

func TestDecodeBookmarks(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected Bookmarks
		wantErr  bool
	}{
		{
			name:     "object format",
			body:     `{"user":{},"bookmarks":[{"bookmark_id":1,"title":"A"},{"bookmark_id":2,"title":"B"}]}`,
			expected: Bookmarks{{BookmarkID: 1, Title: "A"}, {BookmarkID: 2, Title: "B"}},
		},
		{
			name:     "array format",
			body:     `[{"type":"user"},{"type":"bookmark","bookmark_id":1,"title":"A"},{"type":"highlight"}]`,
			expected: Bookmarks{{BookmarkID: 1, Title: "A"}},
		},
		{
			name:    "error array",
			body:    `[{"error_code":1240,"message":"Rate limit exceeded"}]`,
			wantErr: true,
		},
		{
			name:     "empty object",
			body:     `{}`,
			expected: Bookmarks{},
		},
		{
			name:     "empty array",
			body:     `[]`,
			expected: Bookmarks{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := decodeBookmarks(strings.NewReader(c.body))
			if (err != nil) != c.wantErr {
				t.Fatalf("decodeBookmarks() error = %v, wantErr %v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("decodeBookmarks() = %v, want %v", got, c.expected)
			}
		})
	}
}

func TestFilterByTag(t *testing.T) {
	cases := []struct {
		name     string
		tag      string
		input    Bookmarks
		expected Bookmarks
	}{
		{
			name:     "empty tag returns all",
			tag:      "",
			input:    Bookmarks{{BookmarkID: 1}, {BookmarkID: 2}},
			expected: Bookmarks{{BookmarkID: 1}, {BookmarkID: 2}},
		},
		{
			name: "keeps matching bookmarks",
			tag:  "remarkable",
			input: Bookmarks{
				{BookmarkID: 1, Tags: tag("remarkable")},
				{BookmarkID: 2, Tags: tag("other")},
				{BookmarkID: 3, Tags: tag("remarkable")},
			},
			expected: Bookmarks{
				{BookmarkID: 1, Tags: tag("remarkable")},
				{BookmarkID: 3, Tags: tag("remarkable")},
			},
		},
		{
			name:     "no matches returns empty",
			tag:      "missing",
			input:    Bookmarks{{BookmarkID: 1, Tags: tag("remarkable")}},
			expected: Bookmarks{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := filterByTag(c.input, c.tag)
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("filterByTag(%v, %q) = %v, want %v", c.input, c.tag, got, c.expected)
			}
		})
	}
}
