// Package epub handles turning raw Instapaper HTML into valid EPUB XHTML and
// embedding remote images locally.
package epub

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	goepub "github.com/go-shiori/go-epub"
	"golang.org/x/net/html"
)

const (
	maxImageAttempts = 4
	userAgent        = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
)

// Epub wraps github.com/go-shiori/go-epub.Epub and configures a sane HTTP
// client for downloading images.
type Epub struct {
	*goepub.Epub
}

// New creates a new Epub with a custom HTTP client suitable for fetching
// remote images.
func New(title string) (*Epub, error) {
	inner, err := goepub.NewEpub(title)
	if err != nil {
		return nil, err
	}
	inner.Client = &http.Client{
		Timeout: 30 * time.Second,
	}
	return &Epub{inner}, nil
}

var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// SanitizeXHTML parses an HTML fragment and re-serializes it as well-formed
// XHTML. In particular, void elements such as <img> and <br> are written as
// self-closing tags so that go-epub's XML-based sections pass epubcheck.
func SanitizeXHTML(input string) (string, error) {
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	body := findBody(doc)
	if body == nil {
		body = doc
	}

	var buf bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		renderXHTML(c, &buf)
	}
	return buf.String(), nil
}

// PrepareBody sanitizes raw HTML and embeds its images in one step.
func (e *Epub) PrepareBody(rawHTML string, baseURL string, imageDir string) (string, error) {
	safe, err := SanitizeXHTML(rawHTML)
	if err != nil {
		return "", err
	}
	return EmbedImages(e, safe, baseURL, imageDir)
}

// EmbedImages finds <img> tags in the XHTML body, downloads each image, adds
// it to the EPUB, and rewrites the tag's src (and srcset) to the local
// ../images/... path. Downloaded images are written to imageDir so they remain
// available until the EPUB is written. Failures are logged and the original URL
// is left in place.
func EmbedImages(e *Epub, body string, baseURL string, imageDir string) (string, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("parse body for images: %w", err)
	}

	bodyNode := findBody(doc)
	if bodyNode == nil {
		bodyNode = doc
	}

	walkImages(e, bodyNode, baseURL, imageDir)

	var buf bytes.Buffer
	for c := bodyNode.FirstChild; c != nil; c = c.NextSibling {
		renderXHTML(c, &buf)
	}
	return buf.String(), nil
}

func walkImages(e *Epub, n *html.Node, baseURL string, imageDir string) {
	if n.Type == html.ElementNode && n.Data == "img" {
		processImg(e, n, baseURL, imageDir)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkImages(e, c, baseURL, imageDir)
	}
}

func processImg(e *Epub, n *html.Node, baseURL string, imageDir string) {
	src := getAttr(n, "src")
	dataSrc := getAttr(n, "data-src")

	// Prefer lazy-loaded sources.
	target := dataSrc
	if target == "" {
		target = src
	}
	if target != "" {
		resolved, err := resolveURL(baseURL, target)
		if err != nil {
			log.Printf("resolve image URL %q: %v", target, err)
		} else if !strings.HasPrefix(resolved, "data:") {
			internal, err := downloadAndAddImage(e, resolved, imageDir)
			if err != nil {
				log.Printf("download image %q: %v", resolved, err)
			} else {
				setAttr(n, "src", internal)
				if dataSrc != "" {
					delAttr(n, "data-src")
				}
			}
		}
	}

	if srcset := getAttr(n, "srcset"); srcset != "" {
		newSrcset, err := processSrcset(e, baseURL, srcset, imageDir)
		if err != nil {
			log.Printf("process srcset: %v", err)
		} else {
			setAttr(n, "srcset", newSrcset)
		}
	}
}

func processSrcset(e *Epub, baseURL, srcset string, imageDir string) (string, error) {
	entries := strings.Split(srcset, ",")
	var out []string
	for _, entry := range entries {
		fields := strings.Fields(strings.TrimSpace(entry))
		if len(fields) == 0 {
			continue
		}
		urlStr := fields[0]
		descriptor := ""
		if len(fields) > 1 {
			descriptor = strings.Join(fields[1:], " ")
		}

		resolved, err := resolveURL(baseURL, urlStr)
		if err != nil {
			return "", err
		}

		internal := urlStr
		if !strings.HasPrefix(resolved, "data:") {
			p, err := downloadAndAddImage(e, resolved, imageDir)
			if err != nil {
				log.Printf("download srcset image %q: %v", resolved, err)
			} else {
				internal = p
			}
		}

		if descriptor == "" {
			out = append(out, internal)
		} else {
			out = append(out, internal+" "+descriptor)
		}
	}
	return strings.Join(out, ", "), nil
}

func downloadAndAddImage(e *Epub, imageURL string, imageDir string) (string, error) {
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	for attempt := range maxImageAttempts {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			const maxBackoff = 30 * time.Second
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			jitter := time.Duration(rand.Int63n(int64(backoff)))
			time.Sleep(backoff + jitter)
		}

		req, err := http.NewRequest(http.MethodGet, imageURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", userAgent)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode >= http.StatusBadRequest {
			resp.Body.Close()
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}

		tmp, err := os.CreateTemp(imageDir, "img-*.tmp")
		if err != nil {
			resp.Body.Close()
			return "", err
		}
		tmpName := tmp.Name()

		written, err := io.Copy(tmp, resp.Body)
		if closeErr := tmp.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		resp.Body.Close()
		if err != nil {
			os.Remove(tmpName)
			return "", err
		}
		if written == 0 {
			os.Remove(tmpName)
			return "", fmt.Errorf("empty response")
		}

		mimeType, err := mimetype.DetectFile(tmpName)
		if err != nil {
			os.Remove(tmpName)
			return "", err
		}
		ext := mimeType.Extension()
		if ext == "" {
			ext = strings.ToLower(filepath.Ext(imageURL))
		}
		if ext == "" {
			ext = ".bin"
		}

		finalName := strings.TrimSuffix(tmpName, ".tmp") + ext
		if err := os.Rename(tmpName, finalName); err != nil {
			os.Remove(tmpName)
			return "", err
		}

		return e.AddImage(finalName, "")
	}

	return "", fmt.Errorf("failed after %d attempts: %w", maxImageAttempts, lastErr)
}

func resolveURL(base, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.Data == "body" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

func renderXHTML(n *html.Node, buf *bytes.Buffer) {
	switch n.Type {
	case html.TextNode:
		buf.WriteString(html.EscapeString(n.Data))
	case html.ElementNode:
		buf.WriteByte('<')
		buf.WriteString(n.Data)
		for _, a := range n.Attr {
			buf.WriteByte(' ')
			buf.WriteString(a.Key)
			buf.WriteString(`="`)
			buf.WriteString(html.EscapeString(a.Val))
			buf.WriteByte('"')
		}
		if voidElements[n.Data] {
			buf.WriteString(" />")
			return
		}
		buf.WriteByte('>')
		if n.Data == "script" || n.Data == "style" || n.Data == "noscript" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					buf.WriteString(c.Data)
				}
			}
		} else {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				renderXHTML(c, buf)
			}
		}
		buf.WriteString("</")
		buf.WriteString(n.Data)
		buf.WriteByte('>')
	case html.CommentNode:
		buf.WriteString("<!--")
		buf.WriteString(n.Data)
		buf.WriteString("-->")
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderXHTML(c, buf)
		}
	}
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func delAttr(n *html.Node, key string) {
	for i := 0; i < len(n.Attr); {
		if n.Attr[i].Key == key {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			continue
		}
		i++
	}
}
