package sandbox

import "context"

type FakeSandbox struct{}

func (FakeSandbox) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	// Respect cancellation even in the stub, so pool/timeout wiring is testable.
	if err := ctx.Err(); err != nil {
		return RunResult{Status: StatusInternalError}, err
	}
	return RunResult{
		Stdout:   spec.Code,
		ExitCode: 0,
		Status:   StatusOK,
	}, nil
}
