package gitreview

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// png is the smallest thing http.DetectContentType calls a PNG.
var png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

// withPictures gives the harness's Studio a computer with two pictures in /workspace.
func withPictures(t *testing.T, svc *Service) {
	t.Helper()
	svc.PictureDir = t.TempDir()
	svc.Pictures = func(_ context.Context, _ string, p string) ([]byte, string, error) {
		switch p {
		case "/workspace/shot.png", "/workspace/web/after.png":
			return png, "image/png", nil
		case "/workspace/notes.txt":
			return []byte("text"), "text/plain; charset=utf-8", nil
		}
		return nil, "", errors.New(p + " isn't there.")
	}
}

func pictureCopies(t *testing.T, svc *Service) int {
	t.Helper()
	entries, err := os.ReadDir(svc.PictureDir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// A comment's pictures are copied when the agent asks, and uploaded with the user's token once
// approved: only those the comment, as the user edited it, still places.
func TestCommentPicturesUploadOnceApproved(t *testing.T) {
	svc, st, sb, fg := newResolveHarness(t)
	withPictures(t, svc)
	ctx := context.Background()

	if _, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"group/sub/web","number":7,"body":"See","attach":["gone.png"]}`); !integrations.IsToolError(err) || !strings.Contains(err.Error(), "/workspace/gone.png isn't there") {
		t.Fatalf("a missing picture: %v", err)
	}
	if _, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"group/sub/web","number":7,"body":"{{attach:notes.txt}}"}`); !integrations.IsToolError(err) || !strings.Contains(err.Error(), "isn't a PNG") {
		t.Fatalf("not a picture: %v", err)
	}
	if n := pictureCopies(t, svc); n != 0 {
		t.Fatalf("copies of refused asks: %d", n)
	}

	got, err := callReviewTool(t, svc.toolPullRequestComment, sb, `{"repository":"group/sub/web","number":7,"body":"Before: {{attach:shot.png}}","attach":["/workspace/web/after.png","shot.png"]}`)
	if err != nil || got["status"] != "awaiting approval" {
		t.Fatalf("comment: %v %v", got, err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	d, err := CommentDetail(pending[0])
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "Before: {{attach:/workspace/shot.png}}\n\n{{attach:/workspace/web/after.png}}" {
		t.Fatalf("body %q", d.Body)
	}
	if len(d.Pictures) != 2 || d.Pictures[0].Path != "/workspace/shot.png" || d.Pictures[1].MIMEType != "image/png" || pictureCopies(t, svc) != 2 {
		t.Fatalf("pictures %+v", d.Pictures)
	}
	if f, err := svc.PictureFile(ApprovalPictures(pending[0])[1]); err != nil || !strings.HasSuffix(f, ".png") {
		t.Fatalf("picture file %q %v", f, err)
	}

	// The user can't place a picture the agent didn't attach.
	edit := func(body string) integrations.Decision {
		return integrations.Decision{Action: integrations.Allow, Comment: &integrations.CommentEdit{Body: body}}
	}
	if err := svc.decideComment(ctx, pending[0], edit("{{attach:/workspace/other.png}}")); !errors.Is(err, integrations.ErrAction) {
		t.Fatalf("an unknown picture: %v", err)
	}
	if err := svc.decideComment(ctx, pending[0], edit("Before: {{attach:/workspace/shot.png}} and again {{attach:/workspace/shot.png}}")); err != nil {
		t.Fatal(err)
	}
	if len(fg.posted) != 2 || fg.posted[0] != "POST /uploads shot.png 40 image/png" || fg.posted[1] != "POST /notes" {
		t.Fatalf("writes: %v", fg.posted)
	}
	if b := fg.bodies[1]["body"]; b != "Before: ![shot](/uploads/abc/shot.png) and again ![shot](/uploads/abc/shot.png)" {
		t.Fatalf("posted %q", b)
	}
	if n := pictureCopies(t, svc); n != 0 {
		t.Fatalf("copies after posting: %d", n)
	}
}

// A pull request's description takes pictures on GitLab before it is opened; a review's summary
// and line comments place them as a comment does.
func TestPullRequestAndReviewPictures(t *testing.T) {
	svc, st, sb, fg := newResolveHarness(t)
	withPictures(t, svc)
	ctx := context.Background()
	got, err := callReviewTool(t, svc.toolPullRequestReview, sb, `{"repository":"group/sub/web","number":7,"verdict":"comment","summary":"Looks off","comments":[{"path":"a.go","line":2,"body":"Here: {{attach:shot.png}}"}],"attach":["web/after.png"]}`)
	if err != nil || got["status"] != "awaiting approval" {
		t.Fatalf("review: %v %v", got, err)
	}
	pending, _ := st.Approvals(ctx, sb.EnvironmentID, store.StatusPending, 10)
	var review store.Approval
	for _, a := range pending {
		if a.Kind == KindReview {
			review = a
		}
	}
	pics := ApprovalPictures(review)
	if len(pics) != 2 || pics[0].Path != "/workspace/shot.png" || pics[1].Path != "/workspace/web/after.png" {
		t.Fatalf("review pictures %+v", pics)
	}
	if err := svc.decideReview(ctx, review, integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	uploads := 0
	for _, p := range fg.posted {
		if strings.HasPrefix(p, "POST /uploads") {
			uploads++
		}
	}
	if uploads != 2 || pictureCopies(t, svc) != 0 {
		t.Fatalf("review writes: %v", fg.posted)
	}
	var placed []string
	for _, b := range fg.bodies {
		for _, v := range b {
			if s, ok := v.(string); ok && strings.Contains(s, "](/uploads/abc/") {
				placed = append(placed, s)
			}
		}
	}
	if len(placed) != 2 || !strings.Contains(strings.Join(placed, "|"), "Here: ![shot](/uploads/abc/shot.png)") ||
		!strings.Contains(strings.Join(placed, "|"), "Looks off\n\n![after](/uploads/abc/after.png)") {
		t.Fatalf("placed: %q (bodies %v)", placed, fg.bodies)
	}
}

func TestPicturesOnForges(t *testing.T) {
	for _, c := range []struct {
		kind string
		open bool
		ok   bool
	}{
		{KindGitLab, false, true},
		{KindGitLab, true, true},
		{KindForgejo, true, true},
		{KindForgejo, false, false},
		{KindGitHub, true, false},
	} {
		err := picturesOn(store.Forge{Name: "f", Kind: c.kind}, c.open)
		if (err == nil) != c.ok {
			t.Errorf("%s open=%v: %v", c.kind, c.open, err)
		}
	}
	// An ssh forge takes pictures as its API's kind does.
	if err := picturesOn(store.Forge{Name: "f", Kind: KindSSH, APIKind: KindGitLab, APISecretID: "s"}, false); err != nil {
		t.Errorf("ssh with GitLab's API: %v", err)
	}
}

func TestAttachPicturesPlacesTheUnplacedAtTheEnd(t *testing.T) {
	body, files, err := attachPictures("A {{attach: ./a.png }} B {{attach:/tmp/b.gif}}", []string{"{{attach:c.png}}"}, []string{"/workspace/a.png", "d.png"})
	if err != nil {
		t.Fatal(err)
	}
	if body != "A {{attach:/workspace/a.png}} B {{attach:/tmp/b.gif}}\n\n{{attach:/workspace/d.png}}" {
		t.Fatalf("body %q", body)
	}
	if strings.Join(files, " ") != "/workspace/a.png /tmp/b.gif /workspace/c.png /workspace/d.png" {
		t.Fatalf("files %v", files)
	}
	many := make([]string, maxPictures+1)
	for i := range many {
		many[i] = string(rune('a'+i)) + ".png"
	}
	if _, _, err := attachPictures("", nil, many); err == nil {
		t.Fatal("too many pictures")
	}
}
