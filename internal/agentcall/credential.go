package agentcall

import (
	"context"
	"io"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// CredentialCall asks Studio for a short-lived credential made from a secret with a kind, in
// the answer format a tool's helper prints (docs/network.md#credentials). It stays out of
// Tools: agents use the tools, and the tools ask through their helpers.
var CredentialCall = Tool{agentproto.MethodCredential,
	"A tool's credential helper asks for a short-lived credential made from a secret.",
	obj([]string{"secret", "format"}, map[string]any{
		"secret": str("The secret's name", "maxLength", 64),
		"format": str("The answer's format, as the secret's kind names them, such as process", "maxLength", 32),
	})}

// CredentialAnswer is what the helper prints for the tool.
type CredentialAnswer struct {
	Output string `json:"output"`
}

// RunCredential is `studio-agent credential <secret> <format>`: it prints the credential Studio
// issues the sandbox for the secret, in the format the tool's helper interface reads, such as
// AWS's credential_process JSON. Studio may ask the user first, which the call waits for.
func RunCredential(ctx context.Context, c Caller, secret, format string, stdout io.Writer) error {
	var answer CredentialAnswer
	if err := c.Call(ctx, agentproto.MethodCredential, map[string]string{"secret": secret, "format": format}, &answer); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, answer.Output)
	return err
}
