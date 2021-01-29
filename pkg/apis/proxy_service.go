package apis

import (
	"context"

	"github.com/rancher/wins/pkg/panics"
	"github.com/rancher/wins/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type proxyService struct {
}

func (s *proxyService) Publish(context.Context, *types.ProxyPublishRequest) (resp *types.ProxyPublishResponse, respErr error) {
	defer panics.DealWith(func(recoverObj interface{}) {
		respErr = status.Errorf(codes.Unknown, "panic %v", recoverObj)
	})

	// Register the ports for wins

	return &types.ProxyPublishResponse{
		Success: true,
	}, nil
}
