package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Pictures in agents' messages. An agent shows the user a picture, such as a screenshot of
// what it changed, by writing it into its message as markdown: ![what it shows](/a/path.png).
// Once the message is written Studio copies each such file out of the sandbox into its
// chat-images folder (Service.Images), so the thread keeps the picture after the file changes
// or the sandbox is stopped or gone. The web shows it from there (Image); one not copied, as
// when the sandbox was stopped, is tried again when someone looks at it.

// MaxImage is the largest picture Studio copies out of a sandbox.
const MaxImage = 8 << 20

// smallRead is the most a guest from before FilesReadRange can send in one reply without
// waiting on the stream window (docs/guest-agent.md).
const smallRead = 180 << 10

// imageRef finds ![alt](src), ![alt](<src with spaces>) and either with a "title", the alt text
// at most 500 characters as the chat shows it (web/src/components/chat/markdown.tsx).
var imageRef = regexp.MustCompile(`!\[[^\]\n]{0,500}\]\((?:<([^>\n]+)>|([^)\s]+))(?:\s+"[^"\n]*")?\)`)

// imageTypes are the pictures a message shows, with the extension each is kept under.
var imageTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// ImageSources lists the pictures text shows, as written, each once, in order.
func ImageSources(text string) []string {
	var out []string
	for _, m := range imageRef.FindAllStringSubmatch(text, -1) {
		src := m[1] + m[2]
		if !slices.Contains(out, src) {
			out = append(out, src)
		}
	}
	return out
}

// imagePath is where a picture's src is in the sandbox: as written when it is absolute, after
// file://, and in /workspace otherwise; "" for a URL.
func imagePath(src string) string {
	src = strings.TrimPrefix(src, "file://")
	if src == "" || strings.Contains(src, "://") || strings.HasPrefix(src, "//") || strings.HasPrefix(src, "data:") {
		return ""
	}
	if !strings.HasPrefix(src, "/") {
		src = "/workspace/" + src
	}
	return path.Clean(src)
}

// imageError is why a picture wasn't copied, in words for the user.
type imageError string

func (e imageError) Error() string { return string(e) }

// keepImages copies the pictures of an agent's finished message, in the background.
func (s *Service) keepImages(chatID, sandboxID, itemID, text string) {
	srcs := ImageSources(text)
	if len(srcs) == 0 || s.Images == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, src := range srcs {
			if _, err := s.keepImage(ctx, chatID, sandboxID, itemID, src, false); err != nil {
				s.log().Warn("chat: keeping a picture", "chat", chatID, "src", src, "err", err)
			}
		}
	}()
}

// Image returns the file a picture of an agent's message is kept in, and its type, copying it
// out of the sandbox first if it wasn't yet. src must be a picture the message shows. A picture
// that can't be shown is an error whose message says why, for the user.
func (s *Service) Image(ctx context.Context, envID, chatID, itemID, src string) (file, mimeType string, err error) {
	c, err := s.Store.Chat(ctx, envID, chatID)
	if err != nil {
		return "", "", err
	}
	it, err := s.Store.ChatItem(ctx, chatID, itemID)
	if err != nil {
		return "", "", err
	}
	if v := ItemView(it); it.Kind != KindAgent || v.Text == nil || !slices.Contains(ImageSources(v.Text.Text), src) {
		return "", "", store.ErrNotFound
	}
	if s.Images == "" {
		return "", "", imageError("This Studio keeps no pictures.")
	}
	img, err := s.keepImage(ctx, chatID, c.SandboxID, itemID, src, true)
	if err != nil {
		return "", "", err
	}
	if img.File == "" {
		return "", "", imageError(img.Error)
	}
	return filepath.Join(s.Images, img.File), img.MimeType, nil
}

// IsImageError says whether err is why a picture can't be shown, and not a failure.
func IsImageError(err error) bool {
	var e imageError
	return errors.As(err, &e)
}

// keepImage copies a picture out of the sandbox, unless it was copied already or, unless
// again, tried. One copy of a picture runs at a time; others wait for it.
func (s *Service) keepImage(ctx context.Context, chatID, sandboxID, itemID, src string, again bool) (store.ChatImage, error) {
	key := itemID + "\x00" + src
	for {
		s.mu.Lock()
		if s.copying == nil {
			s.copying = map[string]chan struct{}{}
		}
		wait, busy := s.copying[key]
		if !busy {
			done := make(chan struct{})
			s.copying[key] = done
			s.mu.Unlock()
			defer func() {
				s.mu.Lock()
				delete(s.copying, key)
				s.mu.Unlock()
				close(done)
			}()
			break
		}
		s.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return store.ChatImage{}, ctx.Err()
		}
	}
	img, err := s.Store.ChatImage(ctx, itemID, src)
	switch {
	case err == nil && (img.File != "" || !again):
		return img, nil
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return img, err
	}
	img = store.ChatImage{ItemID: itemID, Src: src, ChatID: chatID}
	data, mimeType, err := s.readImage(ctx, sandboxID, imagePath(src))
	if err == nil {
		img.File, err = s.saveImage(data, mimeType)
	}
	if err != nil {
		img.Error = imageMessage(src, err)
	} else {
		img.MimeType, img.Size = mimeType, int64(len(data))
	}
	if err := s.Store.PutChatImage(ctx, img); err != nil {
		s.removeImage(img.File)
		return img, err
	}
	return img, nil
}

// imageMessage says why src wasn't copied.
func imageMessage(src string, err error) string {
	var e imageError
	switch {
	case errors.As(err, &e):
		return e.Error()
	case errors.Is(err, agentchan.ErrNotConnected):
		return "The computer wasn't running, so Studio couldn't copy " + src + " from it. Start it to see the picture."
	}
	return "Studio couldn't copy " + src + ": " + err.Error()
}

// readImage reads a picture out of the sandbox in parts that each fit one reply.
func (s *Service) readImage(ctx context.Context, sandboxID, p string) ([]byte, string, error) {
	if p == "" {
		return nil, "", imageError("Only a file on the computer can be shown here.")
	}
	root, rel := agentproto.FilesRootFS, strings.TrimPrefix(p, "/")
	var data []byte
	for {
		r, err := s.Guest.Files(ctx, sandboxID, agentproto.FilesRequest{Op: agentproto.FilesReadRange, Root: root, Path: rel,
			Offset: int64(len(data)), Limit: agentproto.MaxRangeBytes})
		switch {
		case err != nil && len(data) == 0 && olderGuest(err):
			return s.readSmallImage(ctx, sandboxID, p)
		case err != nil:
			return nil, "", fileError(p, err)
		case r.Unreadable:
			return nil, "", imageError(p + " can't be read by the agent's account.")
		case r.Size > MaxImage:
			return nil, "", imageError(p + " is larger than 8 MiB.")
		case len(data) == 0:
			if _, ok := imageTypes[http.DetectContentType(r.Data)]; !ok {
				return nil, "", notPicture(p)
			}
		}
		data = append(data, r.Data...)
		if len(r.Data) == 0 || int64(len(data)) >= r.Size || len(data) > MaxImage {
			break
		}
	}
	return picture(p, data)
}

// readSmallImage reads a picture from a guest from before FilesReadRange, which sends a file in
// one reply: only as much as one stream window carries, which it would wait on otherwise.
func (s *Service) readSmallImage(ctx context.Context, sandboxID, p string) ([]byte, string, error) {
	older := imageError("Restart the computer so that Studio can copy " + p + ": its agent is from an older Studio, which sends only pictures up to 180 KB.")
	root, rel := agentproto.FilesRootFS, strings.TrimPrefix(p, "/")
	list, err := s.Guest.Files(ctx, sandboxID, agentproto.FilesRequest{Op: agentproto.FilesList, Root: root, Path: path.Dir(rel)})
	switch {
	case err != nil && olderGuest(err), err == nil && list.Root != root:
		return nil, "", older
	case err != nil:
		return nil, "", fileError(p, err)
	}
	i := slices.IndexFunc(list.Entries, func(e agentproto.FileEntry) bool { return e.Name == path.Base(rel) })
	switch {
	case i < 0:
		return nil, "", imageError(p + " isn't there.")
	case list.Entries[i].Size > smallRead:
		return nil, "", older
	}
	r, err := s.Guest.Files(ctx, sandboxID, agentproto.FilesRequest{Op: agentproto.FilesRead, Root: root, Path: rel})
	switch {
	case err != nil:
		return nil, "", fileError(p, err)
	case r.Unreadable:
		return nil, "", imageError(p + " can't be read by the agent's account.")
	case r.Data == nil:
		return nil, "", notPicture(p)
	}
	return picture(p, r.Data)
}

// picture checks that data is a picture a message shows and returns its type.
func picture(p string, data []byte) ([]byte, string, error) {
	if len(data) > MaxImage {
		return nil, "", imageError(p + " is larger than 8 MiB.")
	}
	t := http.DetectContentType(data)
	if _, ok := imageTypes[t]; !ok {
		return nil, "", notPicture(p)
	}
	return data, t, nil
}

func notPicture(p string) error {
	return imageError(p + " isn't a PNG, JPEG, GIF or WebP picture.")
}

func olderGuest(err error) bool {
	return strings.Contains(err.Error(), "unknown files op") || strings.Contains(err.Error(), "unknown files root")
}

// fileError puts the guest's refusals of a path into words; other errors stay as they are.
func fileError(p string, err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "no such file or directory"):
		return imageError(p + " isn't there.")
	case strings.Contains(msg, "symlink"):
		return imageError(p + " goes through a symlink, which Studio doesn't follow.")
	case strings.Contains(msg, "not a regular file"), strings.Contains(msg, "not a directory"):
		return imageError(p + " isn't a file.")
	}
	return err
}

// saveImage writes a picture into the chat-images folder and returns its name there.
func (s *Service) saveImage(data []byte, mimeType string) (string, error) {
	if err := os.MkdirAll(s.Images, 0o700); err != nil {
		return "", err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(b[:]) + imageTypes[mimeType]
	tmp := filepath.Join(s.Images, "."+name)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(s.Images, name)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

func (s *Service) removeImage(name string) {
	if name != "" && s.Images != "" {
		os.Remove(filepath.Join(s.Images, name))
	}
}

// sweepImages removes the pictures of deleted chats, and copies that never got recorded. It
// runs as Studio starts, before any copy.
func (s *Service) sweepImages(ctx context.Context) error {
	if s.Images == "" {
		return nil
	}
	entries, err := os.ReadDir(s.Images)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept, err := s.Store.ChatImageFiles(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !kept[e.Name()] && e.Type().IsRegular() {
			os.Remove(filepath.Join(s.Images, e.Name()))
		}
	}
	return nil
}
