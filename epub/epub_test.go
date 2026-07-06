package epub

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeXHTML(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "self-closes img",
			input:    `<p><img src="a.jpg" alt="x"></p>`,
			contains: []string{`<img src="a.jpg" alt="x" />`},
		},
		{
			name:     "self-closes br and hr",
			input:    `line<br>break<hr>`,
			contains: []string{`line<br />break<hr />`},
		},
		{
			name:     "extracts body from full document",
			input:    `<html><head><title>T</title></head><body><p>hi</p></body></html>`,
			contains: []string{`<p>hi</p>`},
		},
		{
			name:     "escapes text and attributes",
			input:    `<p>1 & 2</p><img alt="x" src="x?a=1&b=2">`,
			contains: []string{`1 &amp; 2`, `src="x?a=1&amp;b=2"`, `<img alt="x" src="x?a=1&amp;b=2" />`},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SanitizeXHTML(c.input)
			if err != nil {
				t.Fatalf("SanitizeXHTML: %v", err)
			}
			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\noutput: %s", want, got)
				}
			}
		})
	}
}

func makePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func imageServer(t *testing.T) *httptest.Server {
	t.Helper()
	png := makePNG(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
}

func readEpub(t *testing.T, e *Epub) (xhtml string, imageFiles []string) {
	t.Helper()
	var buf bytes.Buffer
	if _, err := e.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "section0001.xhtml") {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			xhtml = string(data)
		}
		if strings.Contains(f.Name, "EPUB/images/") {
			imageFiles = append(imageFiles, f.Name)
		}
	}
	return xhtml, imageFiles
}

func TestPrepareBody(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()

	body := `<h1>Hello</h1><img src="/img.png" alt="test"><p>after</p>`

	e, err := New("Test")
	if err != nil {
		t.Fatal(err)
	}

	bodyWithImages, err := e.PrepareBody(body, srv.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.AddSection(bodyWithImages, "Sec", "", ""); err != nil {
		t.Fatal(err)
	}

	xhtml, imageFiles := readEpub(t, e)

	if xhtml == "" {
		t.Fatal("section xhtml not found in epub")
	}
	if !strings.Contains(xhtml, `<img src="../images/`) {
		t.Errorf("image src was not rewritten to embedded path:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, `" alt="test" />`) {
		t.Errorf("img tag is not self-closing:\n%s", xhtml)
	}
	if len(imageFiles) == 0 {
		t.Error("no image file was embedded")
	}
}

func TestEmbedImagesProducesValidXHTML(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()

	body := `<h1>Hello</h1><img src="/img.png" alt="test"><p>after</p>`
	safe, err := SanitizeXHTML(body)
	if err != nil {
		t.Fatal(err)
	}

	e, err := New("Test")
	if err != nil {
		t.Fatal(err)
	}

	bodyWithImages, err := EmbedImages(e, safe, srv.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.AddSection(bodyWithImages, "Sec", "", ""); err != nil {
		t.Fatal(err)
	}

	xhtml, imageFiles := readEpub(t, e)

	if xhtml == "" {
		t.Fatal("section xhtml not found in epub")
	}
	if !strings.Contains(xhtml, `<img src="../images/`) {
		t.Errorf("image src was not rewritten to embedded path:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, `" alt="test" />`) {
		t.Errorf("img tag is not self-closing:\n%s", xhtml)
	}
	if len(imageFiles) == 0 {
		t.Error("no image file was embedded")
	}
}

func TestEmbedImagesPrefersDataSrc(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()

	// The src URL does not exist on the server, but data-src does.
	body := `<p><img src="/missing.png" data-src="/real.png" alt="x"></p>`
	safe, err := SanitizeXHTML(body)
	if err != nil {
		t.Fatal(err)
	}

	e, err := New("Test")
	if err != nil {
		t.Fatal(err)
	}

	bodyWithImages, err := EmbedImages(e, safe, srv.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.AddSection(bodyWithImages, "Sec", "", ""); err != nil {
		t.Fatal(err)
	}

	xhtml, _ := readEpub(t, e)

	if strings.Contains(xhtml, "data-src") {
		t.Errorf("data-src attribute was not removed:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, `<img src="../images/`) {
		t.Errorf("src was not rewritten to embedded path:\n%s", xhtml)
	}
}

func TestEmbedImagesSrcset(t *testing.T) {
	srv := imageServer(t)
	defer srv.Close()

	body := `<p><img srcset="/small.png 1x, /big.png 2x" alt="x"></p>`
	safe, err := SanitizeXHTML(body)
	if err != nil {
		t.Fatal(err)
	}

	e, err := New("Test")
	if err != nil {
		t.Fatal(err)
	}

	bodyWithImages, err := EmbedImages(e, safe, srv.URL, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.AddSection(bodyWithImages, "Sec", "", ""); err != nil {
		t.Fatal(err)
	}

	xhtml, imageFiles := readEpub(t, e)

	if !strings.Contains(xhtml, "../images/") || !strings.Contains(xhtml, "1x") || !strings.Contains(xhtml, "2x") {
		t.Errorf("srcset was not rewritten:\n%s", xhtml)
	}
	if len(imageFiles) < 2 {
		t.Errorf("expected 2 embedded images, got %d", len(imageFiles))
	}
}
