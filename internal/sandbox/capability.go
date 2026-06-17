package sandbox

import (
	"context"

	"github.com/moby/moby/client"
)

func MemoryLimitEnforced(ctx context.Context, sb Sandbox) bool {
	switch s := sb.(type) {
	case *DockerSandbox:
		info, err := s.client.Info(ctx, client.InfoOptions{})
		if err != nil {
			return false
		}
		return info.Info.SwapLimit
	case *NsjailSandbox:
		return cgroupsActive()
	default:
		return false
	}
}
