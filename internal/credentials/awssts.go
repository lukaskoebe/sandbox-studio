package credentials

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// awsSTS issues AWS session credentials: AssumeRole's when the secret names a role, and
// GetSessionToken's otherwise. Requests are signed with Signature Version 4 here, which is
// little enough that Studio needs no AWS SDK.
type awsSTS struct{}

// stsEndpoint is where STS is asked; tests point it elsewhere.
var stsEndpoint = func(region string) string {
	if region == "" {
		return "https://sts.amazonaws.com/"
	}
	return "https://sts." + region + ".amazonaws.com/"
}

var sessionName = regexp.MustCompile(`[^A-Za-z0-9+=,.@_-]`)

func (awsSTS) Issue(ctx context.Context, in IssueInput) (Issued, error) {
	seconds := int(in.Lifetime / time.Second)
	form := url.Values{"Version": {"2011-06-15"}, "DurationSeconds": {strconv.Itoa(seconds)}}
	if role := in.Fields["roleArn"]; role != "" {
		name := sessionName.ReplaceAllString("studio-"+in.Session, "-")
		form.Set("Action", "AssumeRole")
		form.Set("RoleArn", role)
		form.Set("RoleSessionName", name[:min(len(name), 64)])
	} else {
		form.Set("Action", "GetSessionToken")
	}
	region := in.Fields["region"]
	endpoint := stsEndpoint(region)
	if region == "" {
		region = "us-east-1" // the global endpoint's
	}
	body := form.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return Issued{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signV4(req, body, in.Fields["accessKeyId"], in.Secret, region, "sts", time.Now().UTC())

	client := in.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Issued{}, fmt.Errorf("AWS STS: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Issued{}, fmt.Errorf("AWS STS: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Code    string `xml:"Code"`
				Message string `xml:"Message"`
			} `xml:"Error"`
		}
		if xml.Unmarshal(raw, &e) == nil && e.Error.Code != "" {
			return Issued{}, fmt.Errorf("AWS STS refused %s: %s: %s", form.Get("Action"), e.Error.Code, e.Error.Message)
		}
		return Issued{}, fmt.Errorf("AWS STS answered %s", resp.Status)
	}
	var out struct {
		Result struct {
			Credentials struct {
				AccessKeyID     string `xml:"AccessKeyId"`
				SecretAccessKey string `xml:"SecretAccessKey"`
				SessionToken    string `xml:"SessionToken"`
				Expiration      string `xml:"Expiration"`
			} `xml:"Credentials"`
		} `xml:",any"`
	}
	if err := xml.Unmarshal(raw, &out); err != nil {
		return Issued{}, fmt.Errorf("AWS STS's answer: %w", err)
	}
	c := out.Result.Credentials
	expires, err := time.Parse(time.RFC3339, c.Expiration)
	if err != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" || c.SessionToken == "" {
		return Issued{}, errors.New("AWS STS's answer holds no credentials")
	}
	return Issued{
		Fields:  map[string]string{"accessKeyId": c.AccessKeyID, "secretAccessKey": c.SecretAccessKey, "sessionToken": c.SessionToken},
		Expires: expires,
	}, nil
}

// signV4 signs req, whose body is body, with AWS Signature Version 4.
func signV4(req *http.Request, body, keyID, secret, region, service string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	date := amzDate[:8]
	req.Header.Set("X-Amz-Date", amzDate)
	host := req.URL.Host
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	payload := sha256.Sum256([]byte(body))
	canonical := strings.Join([]string{
		req.Method, path, req.URL.RawQuery,
		"content-type:" + req.Header.Get("Content-Type"), "host:" + host, "x-amz-date:" + amzDate, "",
		"content-type;host;x-amz-date", hex.EncodeToString(payload[:]),
	}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	hashed := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(hashed[:])
	key := []byte("AWS4" + secret)
	for _, part := range []string{date, region, service, "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host;x-amz-date, Signature=%s",
		keyID, scope, hex.EncodeToString(hmacSHA256(key, toSign))))
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
