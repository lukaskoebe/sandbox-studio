package chat

import (
	"context"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
)

// ask is a permission request or question waiting for the user.
type ask struct{}

// RequestPermission answers cancelled until chats raise approvals.
func (l *live) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.Cancelled(), nil
}

// CreateElicitation answers cancel until chats ask questions.
func (l *live) CreateElicitation(context.Context, acp.ElicitationRequest) (acp.ElicitationResponse, error) {
	return acp.ElicitationResponse{Action: acp.ElicitCancel}, nil
}

func (l *live) dismissAsks(*run, string) {}

func (s *Service) recoverApprovals(context.Context) error { return nil }

func (s *Service) settleOpenItems(context.Context, string, string) error { return nil }
