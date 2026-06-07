package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/tpsawant027/runboxd/internal/language"
)

const (
	MaxTimeout      = 10 * time.Second
	MaxMemoryBytes  = 128 * 1024 * 1024 // 128 MiB
	DefaultNanoCPUs = 500_000_000       // 0.5 CPU
	MaxOutputBytes  = 1 * 1024 * 1024   // 1 MiB per stream
)

// limitWriter caps writes at n bytes, silently discarding the rest.
// Returns len(p) always so callers (stdcopy) don't treat truncation as a short write error.
type limitWriter struct {
	buf bytes.Buffer
	n   int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n > 0 {
		take := min(int64(len(p)), lw.n)
		lw.buf.Write(p[:take])
		lw.n -= take
	}
	return len(p), nil
}

const (
	sandboxDir = "/sandbox"
	inputDir   = "/input"
)

type dockerSpec struct {
	filename string
	cmd      []string
	image    string
}

var registry = map[language.Language]dockerSpec{
	language.Python: {
		filename: "main.py",
		cmd:      []string{"python", "/sandbox/main.py"},
		image:    "runboxd-python:latest",
	},
}

var ErrUnsupportedLanguage = errors.New("unsupported language")

func lookupSpec(lang language.Language) (dockerSpec, error) {
	spec, ok := registry[lang]
	if !ok {
		return dockerSpec{}, ErrUnsupportedLanguage
	}
	return spec, nil
}

const ImagePullTimeout = 2 * time.Minute

type DockerSandbox struct {
	client *client.Client
}

func ensureImage(cli *client.Client, image string) error {
	_, err := cli.ImageInspect(context.Background(), image)
	switch {
	case err == nil:
		// Present locally — nothing to pull.
	case errdefs.IsNotFound(err):
		// Pull once, draining to EOF so the pull completes before we return.
		ctx, cancel := context.WithTimeout(context.Background(), ImagePullTimeout)
		defer cancel()
		reader, err := cli.ImagePull(ctx, image, client.ImagePullOptions{})
		if err != nil {
			return fmt.Errorf("image %q not available locally and pull failed (run 'make images'?): %w", image, err)
		}
		defer reader.Close()
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return err
		}
	default:
		// Real failure (daemon down, perms, ...), not a missing image — surface it.
		return err
	}
	return nil
}

// TODO: background-pull many images and gate /readyz instead of blocking.
func NewDockerSandbox() (*DockerSandbox, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = cli.Close()
		}
	}()
	for _, spec := range registry {
		if err := ensureImage(cli, spec.image); err != nil {
			return nil, fmt.Errorf("failed to ensure image availability for %q: %w", spec.image, err)
		}
	}
	ok = true
	return &DockerSandbox{client: cli}, nil
}

func (s *DockerSandbox) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	ds, err := lookupSpec(spec.Language)
	if err != nil {
		return RunResult{}, err
	}
	runResInternalErr := RunResult{Status: StatusInternalError}

	// NOTE: the bind-mount source path is resolved on the Docker daemon host - fine
	// for local dev (daemon on same host), but would break with a remote daemon.
	tmpDir, err := os.MkdirTemp("", "runboxd-*")
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return runResInternalErr, fmt.Errorf("failed to chmod temp dir: %w", err)
	}
	codePath := filepath.Join(tmpDir, ds.filename)
	if err := os.WriteFile(codePath, []byte(spec.Code), 0o644); err != nil {
		return runResInternalErr, fmt.Errorf("failed to write code file: %w", err)
	}

	cfg := &container.Config{
		User:       "65534", // nobody
		Cmd:        ds.cmd,
		Tty:        false,
		WorkingDir: sandboxDir,
	}
	if spec.Stdin != "" {
		cfg.AttachStdin = true
		cfg.OpenStdin = true
		cfg.StdinOnce = true
	}
	hostCfg := getHostConfig(spec, tmpDir)

	resp, err := s.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
		Image:      ds.image,
	})
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to create container: %w", err)
	}
	// Fresh ctx (not the request's): a cancelled/timed-out run must still clean up.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.client.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true})
	}()
	// Attach stdin-only before start (output comes from the logs, so this conn
	// can't deadlock on unread output).
	var attach client.ContainerAttachResult
	if spec.Stdin != "" {
		attach, err = s.client.ContainerAttach(ctx, resp.ID, client.ContainerAttachOptions{
			Stream: true,
			Stdin:  true,
		})
		if err != nil {
			return runResInternalErr, fmt.Errorf("failed to attach stdin: %w", err)
		}
		defer attach.Close()
	}

	if _, err := s.client.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return runResInternalErr, fmt.Errorf("failed to start container: %w", err)
	}
	startedAt := time.Now()

	timeout := spec.Timeout
	if timeout <= 0 || timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	execCtx, execCancel := context.WithTimeout(ctx, timeout)
	defer execCancel()

	if spec.Stdin != "" {
		if _, err := io.Copy(attach.Conn, strings.NewReader(spec.Stdin)); err != nil {
			return runResInternalErr, fmt.Errorf("failed to write stdin: %w", err)
		}
		// Half-close so the program's stdin read sees EOF.
		if err := attach.CloseWrite(); err != nil {
			return runResInternalErr, fmt.Errorf("failed to close stdin: %w", err)
		}
	}

	wait := s.client.ContainerWait(execCtx, resp.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})

	var statusCode int64
	select {
	case err := <-wait.Error:
		if execCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return RunResult{Status: StatusTimeout, ExitCode: -1, Duration: time.Since(startedAt)}, nil
		}
		return runResInternalErr, fmt.Errorf("failed waiting for container: %w", err)
	case res := <-wait.Result:
		statusCode = res.StatusCode
		info, inspectErr := s.client.ContainerInspect(context.Background(), resp.ID, client.ContainerInspectOptions{})
		oomKilled := inspectErr == nil && info.Container.State != nil && info.Container.State.OOMKilled
		// OOMKilled is unreliable on some kernels/Docker versions. On
		// the wait.Result path we never send SIGKILL (timeout exits via wait.Error),
		// network is isolated, and memory is always capped - so exit 137 here means
		// the kernel OOM-killed the process.
		if oomKilled || statusCode == 137 {
			return RunResult{Status: StatusOOM, ExitCode: -1, Duration: time.Since(startedAt)}, nil
		}
	}
	out, err := s.client.ContainerLogs(execCtx, resp.ID, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to get container logs: %w", err)
	}

	stdoutW := &limitWriter{n: MaxOutputBytes}
	stderrW := &limitWriter{n: MaxOutputBytes}
	if _, err := stdcopy.StdCopy(stdoutW, stderrW, out); err != nil {
		return runResInternalErr, fmt.Errorf("failed to demux container output: %w", err)
	}

	return RunResult{
		Stdout:   stdoutW.buf.String(),
		Stderr:   stderrW.buf.String(),
		ExitCode: int(statusCode),
		Status:   statusForExit(statusCode),
		Duration: time.Since(startedAt),
	}, nil
}

func (s *DockerSandbox) Close() error {
	return s.client.Close()
}

func (s *DockerSandbox) Ping(ctx context.Context) error {
	_, err := s.client.Ping(ctx, client.PingOptions{})
	return err
}

func (s *DockerSandbox) Info(ctx context.Context) (SandboxInfo, error) {
	langs := make([]language.Language, 0, len(registry))
	for lang := range registry {
		langs = append(langs, lang)
	}
	return SandboxInfo{Languages: langs}, nil
}

func getHostConfig(spec RunSpec, inputSrc string) *container.HostConfig {
	hc := &container.HostConfig{
		Resources: container.Resources{
			Memory:   MaxMemoryBytes,
			NanoCPUs: DefaultNanoCPUs,
		},
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/sandbox": "size=10m,noexec,mode=1777",
			"/tmp":     "size=5m,noexec,mode=1777",
		},
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   inputSrc,
				Target:   inputDir,
				ReadOnly: true,
			},
		},
	}
	if spec.MemoryBytes > 0 && spec.MemoryBytes < MaxMemoryBytes {
		hc.Memory = spec.MemoryBytes
	}
	hc.MemorySwap = hc.Memory
	return hc
}

func statusForExit(code int64) Status {
	if code == 0 {
		return StatusOK
	}
	return StatusRuntimeError
}
