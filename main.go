package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env"
	"github.com/geesawra/instarm/epub"
	"github.com/geesawra/instarm/instapaper"
	"github.com/geesawra/instarm/remarkable"
)

type credentials struct {
	Key      string `env:"KEY"`
	Secret   string `env:"SECRET"`
	Email    string `env:"EMAIL"`
	Password string `env:"PASSWORD"`
}

type remarkableConfig struct {
	DeviceToken string `env:"RMAPI_DEVICE_TOKEN"`
	UserToken   string `env:"RMAPI_USER_TOKEN"`
	FolderID    string `env:"REMARKABLE_FOLDER_ID"`
}

func main() {
	setup := flag.Bool("setup", false, "authenticate with reMarkable and print tokens")
	listFolders := flag.Bool("list-folders", false, "list reMarkable folders with their IDs")
	flag.Parse()

	if *setup {
		if err := runSetup(); err != nil {
			log.Fatal("setup:", err)
		}
		return
	}

	if *listFolders {
		if err := runListFolders(); err != nil {
			log.Fatal("list folders:", err)
		}
		return
	}

	var c credentials
	if err := env.Parse(&c); err != nil {
		log.Fatal("load credentials:", err)
	}

	var rc remarkableConfig
	if err := env.Parse(&rc); err != nil {
		log.Fatal("load remarkable config:", err)
	}
	if rc.DeviceToken == "" || rc.UserToken == "" {
		log.Fatal("RMAPI_DEVICE_TOKEN and RMAPI_USER_TOKEN are required")
	}

	ic, err := instapaper.New(c.Key, c.Secret, c.Email, c.Password)
	if err != nil {
		log.Fatal("create instapaper client:", err)
	}

	rm, err := remarkable.New(remarkable.Config{
		DeviceToken: rc.DeviceToken,
		UserToken:   rc.UserToken,
	})
	if err != nil {
		log.Fatal("create remarkable client:", err)
	}

	rc.FolderID = strings.TrimSpace(rc.FolderID)
	folderID, err := rm.ResolveFolderID(rc.FolderID)
	if err != nil {
		log.Fatal("resolve remarkable folder:", err)
	}
	if rc.FolderID != "" {
		log.Printf("using remarkable folder %q (resolved to %q)", rc.FolderID, folderID)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		list, err := ic.Unread("remarkable")
		if err != nil {
			log.Printf("list unread remarkable bookmarks: %v", err)
			continue
		}

		if len(list) == 0 {
			continue
		}

		log.Printf("found %d unread remarkable bookmark(s)", len(list))

		for _, b := range list {
			if err := processBookmark(ic, rm, folderID, b); err != nil {
				log.Printf("process bookmark %q: %v", b.Title, err)
			}
		}
	}
}

func runSetup() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter one-time code (go to https://my.remarkable.com/device/browser/connect): ")
	code, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read code: %w", err)
	}
	code = strings.TrimSpace(code)

	cfg, err := remarkable.Authenticate(code)
	if err != nil {
		return err
	}

	fmt.Printf("export RMAPI_DEVICE_TOKEN=%q\n", cfg.DeviceToken)
	fmt.Printf("export RMAPI_USER_TOKEN=%q\n", cfg.UserToken)
	return nil
}

func runListFolders() error {
	var rc remarkableConfig
	if err := env.Parse(&rc); err != nil {
		return fmt.Errorf("load remarkable config: %w", err)
	}
	if rc.DeviceToken == "" || rc.UserToken == "" {
		return fmt.Errorf("RMAPI_DEVICE_TOKEN and RMAPI_USER_TOKEN are required")
	}

	rm, err := remarkable.New(remarkable.Config{
		DeviceToken: rc.DeviceToken,
		UserToken:   rc.UserToken,
	})
	if err != nil {
		return fmt.Errorf("create remarkable client: %w", err)
	}

	folders := rm.ListFolders()
	if len(folders) == 0 {
		fmt.Println("no folders found")
		return nil
	}

	fmt.Println("ID\t\t\t\t\tPath")
	for _, f := range folders {
		fmt.Printf("%s\t%s\n", f.ID, f.Path)
	}
	return nil
}

func processBookmark(ic *instapaper.Instapaper, rm *remarkable.Client, folderID string, b instapaper.Bookmark) error {
	content, err := ic.HTMLContent(b.BookmarkID)
	if err != nil {
		return fmt.Errorf("fetch content: %w", err)
	}

	e, err := epub.New(b.Title)
	if err != nil {
		return fmt.Errorf("create epub: %w", err)
	}

	imageDir, err := os.MkdirTemp("", "instarm-images-*")
	if err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}
	defer os.RemoveAll(imageDir)

	body, err := e.PrepareBody(content, b.URL, imageDir)
	if err != nil {
		return fmt.Errorf("prepare body: %w", err)
	}

	if _, err := e.AddSection(body, b.Title, "", ""); err != nil {
		return fmt.Errorf("add section: %w", err)
	}

	epubDir, err := os.MkdirTemp("", "instarm-epub-*")
	if err != nil {
		return fmt.Errorf("create epub dir: %w", err)
	}
	defer os.RemoveAll(epubDir)

	epubPath := filepath.Join(epubDir, sanitizeFilename(b.Title)+".epub")
	epubFile, err := os.Create(epubPath)
	if err != nil {
		return fmt.Errorf("create epub file: %w", err)
	}

	if _, err := e.WriteTo(epubFile); err != nil {
		_ = epubFile.Close()
		return fmt.Errorf("write epub: %w", err)
	}

	if err := epubFile.Close(); err != nil {
		return fmt.Errorf("close epub file: %w", err)
	}

	if err := rm.UploadDocument(epubPath, folderID); err != nil {
		return fmt.Errorf("upload to remarkable: %w", err)
	}

	if err := ic.MarkAsRead(b.BookmarkID); err != nil {
		return fmt.Errorf("mark bookmark as read: %w", err)
	}

	log.Printf("uploaded %q to remarkable", b.Title)
	return nil
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		name = "untitled"
	}
	return name
}
