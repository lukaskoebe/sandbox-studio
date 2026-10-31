package credentials

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The signature of a request the AWS CLI (2.39.1) sent with the documentation's example key.
func TestSignV4(t *testing.T) {
	body := "Action=GetSessionToken&Version=2011-06-15&DurationSeconds=3600"
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18977/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	at, _ := time.Parse("20060102T150405Z", "20261030T195544Z")
	signV4(req, body, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "sts", at)
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261030/us-east-1/sts/aws4_request, SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=987f57ade6f6c19beeb3a7f5311a71c3f53c80e8d0b34f74af6b06c0551793dc"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization\n got %s\nwant %s", got, want)
	}
}
