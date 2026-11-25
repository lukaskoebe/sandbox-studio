package gitreview

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Pictures in what a teammate posts on a forge: a pull request's description, a comment on its
// conversation, a review and its line comments. The agent names files on its computer, with
// {{attach:<path>}} where each goes. Studio copies them out of the sandbox when the agent
// asks, so the user approves the pictures the card shows, and once approved uploads the ones
// the text still places with the user's token, putting the forge's Markdown in their places
// (docs/git-review.md#pictures).

// maxPictures bounds the pictures of one ask.
const maxPictures = 10

// pictureRef is a picture's place in a text: {{attach:<path>}}.
var pictureRef = regexp.MustCompile(`\{\{attach:([^{}\n]+)\}\}`)

// pictureName is the name of Studio's copy of a picture.
var pictureName = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|gif|webp)$`)

// pictureExt is each picture type's extension.
var pictureExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// Picture is a picture a draft shows: the file the agent named, and Studio's copy of it.
type Picture struct {
	Path     string `json:"path" doc:"The file on the sandbox's computer, as {{attach:<path>}} names it in the text"`
	MIMEType string `json:"mimeType"`
	Size     int    `json:"size"`
	// File is Studio's copy in its pictures folder, removed once the draft is decided.
	File string `json:"file" doc:"Studio's copy of the picture, until the draft is decided"`
}

// PictureReader reads a picture off a sandbox's computer: PNG, JPEG, GIF or WebP, returning
// its bytes and type. Its errors say in words why it can't.
type PictureReader func(ctx context.Context, sandboxID, path string) ([]byte, string, error)

// Uploader uploads a picture for a pull request's text and returns the Markdown that shows it.
// number is the pull request's, or 0 before it is opened.
type Uploader interface {
	UploadPicture(ctx context.Context, token, owner, repo string, number int, name, mimeType string, data []byte) (string, error)
}

// picturePath is the file a placeholder or an attach argument names: an absolute path, or one
// relative to /workspace.
func picturePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("an attached picture names no file")
	}
	if len(p) > 1024 {
		return "", errors.New("an attached picture's path is longer than 1024 bytes")
	}
	if !path.IsAbs(p) {
		p = path.Join("/workspace", p)
	}
	return path.Clean(p), nil
}

// placedPictures lists the files texts place, each once, in order.
func placedPictures(texts ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range pictureRef.FindAllStringSubmatch(t, -1) {
			p, err := picturePath(m[1])
			if err != nil || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// cleanPlaces writes each placeholder of text with its file's path as picturePath has it, so
// that the draft's pictures and its text name them alike.
func cleanPlaces(text string) string {
	return pictureRef.ReplaceAllStringFunc(text, func(m string) string {
		p, err := picturePath(pictureRef.FindStringSubmatch(m)[1])
		if err != nil {
			return m
		}
		return "{{attach:" + p + "}}"
	})
}

// attachPictures takes an ask's pictures into its text: each placeholder names its file as
// picturePath has it, and each attached file no text places goes at the end of body. It
// returns the body and the files, in order.
func attachPictures(body string, others []string, attach []string) (string, []string, error) {
	body = cleanPlaces(body)
	placed := placedPictures(append([]string{body}, others...)...)
	for _, a := range attach {
		p, err := picturePath(a)
		if err != nil {
			return "", nil, err
		}
		if !slices.Contains(placed, p) {
			body = strings.TrimSpace(body + "\n\n{{attach:" + p + "}}")
			placed = append(placed, p)
		}
	}
	if len(placed) > maxPictures {
		return "", nil, fmt.Errorf("attach at most %d pictures", maxPictures)
	}
	return body, placed, nil
}

// picturesOn says why f can't take the pictures of a pull request, if it can't: open says
// whether the pull request is open already.
func picturesOn(f store.Forge, open bool) error {
	api, _ := apiForge(f)
	switch api.Kind {
	case KindGitLab:
		return nil
	case KindForgejo:
		if open {
			return nil
		}
		return fmt.Errorf("%s takes pictures only on an open pull request: open it without them, then show them in a comment", f.Name)
	case KindGitHub:
		return errors.New("GitHub has no API to upload pictures with: describe what they show, or leave them out")
	}
	return fmt.Errorf("%s has no API to upload pictures with", f.Name)
}

// copyPictures copies the files out of the sandbox into Studio's pictures folder.
func (s *Service) copyPictures(ctx context.Context, sb store.Sandbox, files []string) ([]Picture, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if s.Pictures == nil || s.PictureDir == "" {
		return nil, integrations.ToolError("Studio can't copy pictures out of this sandbox")
	}
	if err := os.MkdirAll(s.PictureDir, 0o700); err != nil {
		return nil, err
	}
	var out []Picture
	for _, p := range files {
		data, mimeType, err := s.Pictures(ctx, sb.ID, p)
		if err == nil && pictureExt[mimeType] == "" {
			err = fmt.Errorf("%s isn't a PNG, JPEG, GIF or WebP picture", p)
		}
		if err != nil {
			s.removePictures(out)
			if ctx.Err() != nil {
				return nil, err
			}
			return nil, integrations.ToolError("%s", err)
		}
		name, err := s.savePicture(data, mimeType)
		if err != nil {
			s.removePictures(out)
			return nil, err
		}
		out = append(out, Picture{Path: p, MIMEType: mimeType, Size: len(data), File: name})
	}
	return out, nil
}

func (s *Service) savePicture(data []byte, mimeType string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(b[:]) + pictureExt[mimeType]
	tmp := filepath.Join(s.PictureDir, "."+name)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(s.PictureDir, name)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// removePictures removes Studio's copies of pictures, once their draft is decided.
func (s *Service) removePictures(ps []Picture) {
	for _, p := range ps {
		if s.PictureDir != "" && pictureName.MatchString(p.File) {
			os.Remove(filepath.Join(s.PictureDir, p.File))
		}
	}
}

// PictureFile returns the file holding Studio's copy of a draft's picture, while it has one.
func (s *Service) PictureFile(p Picture) (string, error) {
	if s.PictureDir == "" || !pictureName.MatchString(p.File) {
		return "", os.ErrNotExist
	}
	f := filepath.Join(s.PictureDir, p.File)
	if _, err := os.Lstat(f); err != nil {
		return "", err
	}
	return f, nil
}

// ApprovalPictures lists the pictures of a git.pr, git.comment or git.review approval.
func ApprovalPictures(a store.Approval) []Picture {
	var v struct {
		Pictures []Picture `json:"pictures"`
	}
	switch a.Kind {
	case KindPR, KindComment, KindReview:
		if json.Unmarshal(a.Payload, &v) == nil {
			return v.Pictures
		}
	}
	return nil
}

// checkPlaces checks that each picture the texts place, as the user edited them, is one of
// the draft's.
func checkPlaces(pics []Picture, texts ...string) error {
	for _, p := range placedPictures(texts...) {
		if !hasPicture(pics, p) {
			return fmt.Errorf("{{attach:%s}} names a picture the agent didn't attach", p)
		}
	}
	return nil
}

func hasPicture(pics []Picture, p string) bool {
	for _, pic := range pics {
		if pic.Path == p {
			return true
		}
	}
	return false
}

// placer uploads the pictures texts place, each once, and puts the forge's Markdown in their
// places.
type placer struct {
	s      *Service
	up     Uploader
	token  string
	owner  string
	repo   string
	number int
	pics   []Picture
	done   map[string]string // path -> Markdown
}

func (s *Service) placer(ad any, f store.Forge, token, owner, repo string, number int, pics []Picture) (*placer, error) {
	if len(pics) == 0 {
		return &placer{}, nil
	}
	up, ok := ad.(Uploader)
	if !ok {
		return nil, fmt.Errorf("%s has no API to upload pictures with", f.Name)
	}
	return &placer{s: s, up: up, token: token, owner: owner, repo: repo, number: number, pics: pics, done: map[string]string{}}, nil
}

// place uploads the pictures text places that aren't up yet and returns text with their
// Markdown in their places.
func (p *placer) place(ctx context.Context, text string) (string, error) {
	if len(p.pics) == 0 || !strings.Contains(text, "{{attach:") {
		return text, nil
	}
	var failed error
	out := pictureRef.ReplaceAllStringFunc(text, func(m string) string {
		if failed != nil {
			return m
		}
		file, err := picturePath(pictureRef.FindStringSubmatch(m)[1])
		if err != nil {
			return m
		}
		if md, ok := p.done[file]; ok {
			return md
		}
		md, err := p.upload(ctx, file)
		if err != nil {
			failed = err
			return m
		}
		p.done[file] = md
		return md
	})
	return out, failed
}

func (p *placer) upload(ctx context.Context, file string) (string, error) {
	for _, pic := range p.pics {
		if pic.Path != file {
			continue
		}
		f, err := p.s.PictureFile(pic)
		if err != nil {
			return "", fmt.Errorf("Studio's copy of %s is gone", file)
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		name := path.Base(file)
		if ext := pictureExt[pic.MIMEType]; !strings.EqualFold(path.Ext(name), ext) && !(ext == ".jpg" && strings.EqualFold(path.Ext(name), ".jpeg")) {
			name += ext
		}
		md, err := p.up.UploadPicture(ctx, p.token, p.owner, p.repo, p.number, name, pic.MIMEType, data)
		if err != nil {
			return "", fmt.Errorf("uploading %s: %w", file, err)
		}
		return md, nil
	}
	return "", fmt.Errorf("{{attach:%s}} names a picture the agent didn't attach", file)
}

// SweepPictures removes the copies of pictures no waiting draft shows: those of drafts decided
// or dropped while Studio wasn't running. A copy younger than an hour stays, as an ask may be
// making its draft.
func (s *Service) SweepPictures(ctx context.Context) error {
	if s.PictureDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.PictureDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, e := range envs {
		pending, err := s.Store.Approvals(ctx, e.ID, store.StatusPending, 10000)
		if err != nil {
			return err
		}
		for _, a := range pending {
			for _, p := range ApprovalPictures(a) {
				keep[p.File] = true
			}
		}
	}
	for _, e := range entries {
		if keep[e.Name()] || !e.Type().IsRegular() {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) < time.Hour {
			continue
		}
		os.Remove(filepath.Join(s.PictureDir, e.Name()))
	}
	return nil
}

// multipartFile is a form with one file, as forges take uploads.
func multipartFile(field, name, mimeType string, data []byte) (rawBody, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	h.Set("Content-Type", mimeType)
	part, err := w.CreatePart(h)
	if err != nil {
		return rawBody{}, err
	}
	if _, err := part.Write(data); err != nil {
		return rawBody{}, err
	}
	if err := w.Close(); err != nil {
		return rawBody{}, err
	}
	return rawBody{contentType: w.FormDataContentType(), data: b.Bytes()}, nil
}

// UploadPicture uploads a picture to the project, which any of its texts can show.
func (g *GitLab) UploadPicture(ctx context.Context, token, owner, repo string, _ int, name, mimeType string, data []byte) (string, error) {
	body, err := multipartFile("file", name, mimeType, data)
	if err != nil {
		return "", err
	}
	var out struct {
		Markdown string `json:"markdown"`
		URL      string `json:"url"`
	}
	if err := g.call(ctx, token, http.MethodPost, "/projects/"+gitlabProject(owner, repo)+"/uploads", body, &out); err != nil {
		return "", err
	}
	switch {
	case out.Markdown != "":
		return out.Markdown, nil
	case out.URL != "":
		return "![" + strings.TrimSuffix(name, path.Ext(name)) + "](" + out.URL + ")", nil
	}
	return "", errors.New("the forge didn't say where the picture went")
}

// UploadPicture attaches a picture to the pull request's issue, which its comments can show.
func (f *Forgejo) UploadPicture(ctx context.Context, token, owner, repo string, number int, name, mimeType string, data []byte) (string, error) {
	if number < 1 {
		return "", errors.New("Forgejo and Gitea take pictures only on an open pull request")
	}
	body, err := multipartFile("attachment", name, mimeType, data)
	if err != nil {
		return "", err
	}
	var out struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	p := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/issues/" + strconv.Itoa(number) + "/assets"
	if err := f.call(ctx, token, http.MethodPost, p, body, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", errors.New("the forge didn't say where the picture went")
	}
	return "![" + strings.TrimSuffix(name, path.Ext(name)) + "](" + out.URL + ")", nil
}
