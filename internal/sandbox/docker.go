package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/registry"
	"golang.org/x/sync/errgroup"
)

const (
	MaxPids                 = 100
	MinTimeout              = 1 * time.Second
	MaxTimeout              = 10 * time.Second
	MinMemoryBytes          = 64 * 1024 * 1024  // 64 MiB
	MaxMemoryBytes          = 256 * 1024 * 1024 // 256 MiB
	DefaultMaxCPUs          = 0.5
	MaxOutputBytes          = 1 * 1024 * 1024 // 1 MiB per stream
	MaxLogConfigFileSize    = "3m"
	MaxLogConfigFileCount   = "1"
	DefaultWorkspaceSizeMiB = 10
	DefaultTmpSizeMiB       = 5

	ImagePullTimeout = 2 * time.Minute
)

const (
	managedLabel = "runboxd.managed"
	reapMaxAge   = time.Minute

	sandboxDir = "/sandbox"
	inputDir   = "/input"
	buildDir   = "/build"
)

const compileFailExitCode = 100

var (
	ErrUnsupportedLanguage = errors.New("unsupported language")
	ErrUnsupportedVersion  = errors.New("unsupported version")
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

func (lw *limitWriter) String() string {
	return lw.buf.String()
}

type dockerSpec struct {
	langType string
	filename string
	image    string
	limits   LangLimits
}

type versionSpec struct {
	image    string
	runCmd   []string
	buildCmd []string
}

type langEntry struct {
	defaultVersion string
	langType       string
	filename       string
	env            map[string]string
	limits         LangLimits
	compileLimits  LangCompileLimits
	versions       map[string]versionSpec // version -> image + run command + build command (if compiled)
}

type DockerSandbox struct {
	client *client.Client
	specs  map[string]langEntry
	logger *slog.Logger
}

var (
	_ Sandbox  = (*DockerSandbox)(nil)
	_ Pinger   = (*DockerSandbox)(nil)
	_ Informer = (*DockerSandbox)(nil)
	_ Reaper   = (*DockerSandbox)(nil)
)

type DockerSandboxConfig struct {
	RegistryPath string
}

var _ SandboxConfig = (*DockerSandboxConfig)(nil)

func (c DockerSandboxConfig) sandboxConfig() {}

// TODO: background-pull many images and gate /readyz instead of blocking.
func NewDockerSandbox(cfg DockerSandboxConfig, logger *slog.Logger) (*DockerSandbox, error) {
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
	registry, err := registry.Load(cfg.RegistryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load registry: %w", err)
	}

	specs := make(map[string]langEntry, len(registry.Languages))

	g, gctx := errgroup.WithContext(context.Background())
	var mu sync.Mutex

	for _, entry := range registry.Languages {
		g.Go(func() error {
			defaultVersion, ok := entry.Versions[entry.DefaultVersion]
			if !ok {
				return fmt.Errorf("default version %q not found for language %q", entry.DefaultVersion, entry.Name)
			}

			limits := resolveLangLimits(entry.Limits)
			if err := validateLangLimits(limits); err != nil {
				return fmt.Errorf("invalid limits for language %q: %w", entry.Name, err)
			}

			spec := langEntry{
				defaultVersion: defaultVersion.Name,
				langType:       entry.Type,
				filename:       entry.Filename,
				env:            entry.Env,
				limits:         limits,
				versions:       make(map[string]versionSpec, len(entry.Versions)),
			}
			for _, version := range entry.Versions {
				spec.versions[version.Name] = versionSpec{image: version.Image, runCmd: version.RunCmd, buildCmd: version.BuildCmd}
				if err := ensureImage(gctx, cli, version.Image); err != nil {
					return fmt.Errorf("failed to ensure image %q for language %q version %q: %w", version.Image, entry.Name, version.Name, err)
				}
			}
			mu.Lock()
			specs[entry.Name] = spec
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	ok = true
	return &DockerSandbox{client: cli, specs: specs, logger: logger}, nil
}

func (s *DockerSandbox) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	ds, err := s.lookupSpec(spec.Language, spec.Version)
	if err != nil {
		return RunResult{}, err
	}

	runResInternalErr := RunResult{Status: StatusInternalError}

	tmpDir, err := setupWorkspace("runboxd-*", spec.Code, ds.filename, spec.WorkspaceFiles)
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to setup workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &container.Config{
		User:       "65534", // nobody
		Tty:        false,
		WorkingDir: sandboxDir,
		Labels:     map[string]string{managedLabel: "1"},
	}

	if spec.Stdin != "" {
		cfg.AttachStdin = true
		cfg.OpenStdin = true
		cfg.StdinOnce = true
	}
	hostCfg := getHostConfig(spec, ds, filepath.Join(tmpDir, inputDir))

	// Detach the create call from the request ctx: if the client disconnects mid-ContainerCreate,
	// the request ctx cancels, the client call returns an error WITHOUT the container ID, and the
	// daemon-side container (already created) is orphaned because the cleanup defer below never registers.
	// WithoutCancel keeps request values but drops its cancellation/deadline; WithTimeout still bounds it.
	// We instead check ctx.Err() after create returns to honor request cancellation/timeout (the defer cleans up).
	createCtx, createCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer createCancel()

	resp, err := s.client.ContainerCreate(createCtx, client.ContainerCreateOptions{
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
		if _, err := s.client.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			s.logger.Warn("failed to remove container", "id", resp.ID, "err", err)
		}
	}()

	if ctx.Err() != nil {
		return runResInternalErr, fmt.Errorf("request context error before start: %w", ctx.Err())
	}

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

	timeout := effectiveTimeout(spec.Timeout, ds.limits)
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
		Stdout:   stdoutW.String(),
		Stderr:   stderrW.String(),
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

func (s *DockerSandbox) LangSpec(language, version string) (LangSpec, error) {
	ds, err := s.lookupSpec(language, version)
	if err != nil {
		return LangSpec{}, err
	}
	return LangSpec{
		Filename: ds.filename,
		Limits:   ds.limits,
	}, nil
}

func (s *DockerSandbox) Info(_ context.Context) (SandboxInfo, error) {
	langs := make([]LanguageInfo, 0, len(s.specs))
	for name, entry := range s.specs {
		versions := make([]string, 0, len(entry.versions))
		for version := range entry.versions {
			versions = append(versions, version)
		}
		langs = append(langs, LanguageInfo{
			Name:           name,
			DefaultVersion: entry.defaultVersion,
			Versions:       versions,
			Filename:       entry.filename,
			Limits:         entry.limits,
		})
	}
	return SandboxInfo{Languages: langs}, nil
}

func (s *DockerSandbox) ReapOrphans(ctx context.Context) {
	s.reapOrphans(ctx, reapMaxAge)
}

func (s *DockerSandbox) lookupSpec(lang, version string) (dockerSpec, error) {
	entry, ok := s.specs[lang]
	if !ok {
		return dockerSpec{}, ErrUnsupportedLanguage
	}
	if version == "" {
		version = entry.defaultVersion
	}
	vs, ok := entry.versions[version]
	if !ok {
		return dockerSpec{}, ErrUnsupportedVersion
	}
	return dockerSpec{
		langType: entry.langType,
		filename: entry.filename,
		image:    vs.image,
		limits:   entry.limits,
	}, nil
}

func (s *DockerSandbox) reapOrphans(ctx context.Context, maxAge time.Duration) {
	res, err := s.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", managedLabel),
	})
	if err != nil {
		s.logger.Warn("reaper: list failed", "err", err)
		return
	}
	now := time.Now()
	for _, c := range res.Items {
		if !isOrphan(c.Created, now, maxAge) {
			continue
		}
		rmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if _, err := s.client.ContainerRemove(rmCtx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			s.logger.Warn("reaper: remove failed", "id", c.ID, "err", err)
		}
		cancel()
	}
}

func ensureImage(ctx context.Context, cli *client.Client, image string) error {
	_, err := cli.ImageInspect(ctx, image)
	switch {
	case err == nil:
		// Present locally - nothing to pull.
	case errdefs.IsNotFound(err):
		// Pull once, draining to EOF so the pull completes before we return.
		tctx, cancel := context.WithTimeout(ctx, ImagePullTimeout)
		defer cancel()
		reader, err := cli.ImagePull(tctx, image, client.ImagePullOptions{})
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

// valueWithDefault returns defaultValue when value is the zero value of T.
// DO NOT use for fields where zero is a valid explicit value.
func valueWithDefault[T comparable](value, defaultValue T) T {
	var zero T
	if value == zero {
		return defaultValue
	}
	return value
}

// resolveLangLimits converts the registry's ergonomic limits (MiB/seconds, zero =
// unset) into resolved LangLimits, filling unset fields from the package-const
// defaults. Max is resolved first; an unset min defaults to the package floor but
// is clamped to never exceed the resolved max (so setting only a low max doesn't
// trip the min<=max check). An explicit min>max is left intact for validation.
func resolveLangLimits(l imagespec.Limits) LangLimits {
	maxTimeout := valueWithDefault(time.Duration(l.MaxTimeoutSeconds)*time.Second, MaxTimeout)
	maxMemory := valueWithDefault(int64(l.MaxMemoryMiB)*1024*1024, MaxMemoryBytes)

	minTimeout := time.Duration(l.MinTimeoutSeconds) * time.Second
	if minTimeout == 0 {
		minTimeout = min(MinTimeout, maxTimeout)
	}
	minMemory := int64(l.MinMemoryMiB) * 1024 * 1024
	if minMemory == 0 {
		minMemory = min(MinMemoryBytes, maxMemory)
	}

	return LangLimits{
		MinTimeout:         minTimeout,
		MaxTimeout:         maxTimeout,
		MinMemoryBytes:     minMemory,
		MaxMemoryBytes:     maxMemory,
		MaxPids:            valueWithDefault(int64(l.MaxPids), MaxPids),
		MaxCPUs:            valueWithDefault(l.MaxCPUs, DefaultMaxCPUs),
		WorkspaceSizeBytes: valueWithDefault(int64(l.WorkspaceSizeMiB), DefaultWorkspaceSizeMiB) * 1024 * 1024,
		TmpSizeBytes:       valueWithDefault(int64(l.TmpSizeMiB), DefaultTmpSizeMiB) * 1024 * 1024,
	}
}

func validateLangLimits(limits LangLimits) error {
	if limits.MinTimeout < 0 || limits.MaxTimeout < 0 {
		return fmt.Errorf("timeout limits must be non-negative")
	}
	if limits.MinMemoryBytes < 0 || limits.MaxMemoryBytes < 0 {
		return fmt.Errorf("memory limits must be non-negative")
	}
	if limits.MaxPids < 1 {
		return fmt.Errorf("MaxPids must be at least 1")
	}
	// Reject non-positive AND non-finite: 0/negative -> 0 = UNLIMITED at the
	// backend; NaN/+Inf slip past a bare `<= 0` and convert to a garbage int64.
	if !(limits.MaxCPUs > 0) || math.IsInf(limits.MaxCPUs, 1) {
		return fmt.Errorf("MaxCPUs must be a positive, finite number")
	}

	if limits.WorkspaceSizeBytes <= 0 {
		return fmt.Errorf("WorkspaceSizeBytes must be positive")
	}

	if limits.TmpSizeBytes <= 0 {
		return fmt.Errorf("TmpSizeBytes must be positive")
	}

	if limits.MinTimeout > limits.MaxTimeout {
		return fmt.Errorf("MinTimeout cannot be greater than MaxTimeout")
	}
	if limits.MinMemoryBytes > limits.MaxMemoryBytes {
		return fmt.Errorf("MinMemoryBytes cannot be greater than MaxMemoryBytes")
	}

	return nil
}

func isOrphan(created int64, now time.Time, maxAge time.Duration) bool {
	return created <= now.Add(-maxAge).Unix()
}

func setupWorkspace(tmpDirPattern, code, codeFilename string, workspaceFiles []WorkspaceFile) (tmpDir string, err error) {
	defer func() {
		if err != nil && tmpDir != "" {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	// NOTE: the bind-mount source path is resolved on the Docker daemon host - fine
	// for local dev (daemon on same host), but would break with a remote daemon.
	tmpDir, err = os.MkdirTemp("", tmpDirPattern)
	if err != nil {
		return tmpDir, fmt.Errorf("failed to create temp dir: %w", err)
	}
	if err := os.Chmod(tmpDir, 0o755); err != nil {
		return tmpDir, fmt.Errorf("failed to chmod temp dir: %w", err)
	}
	inputRoot := filepath.Join(tmpDir, inputDir)
	if err := os.MkdirAll(inputRoot, 0o755); err != nil {
		return tmpDir, fmt.Errorf("failed to create input dir: %w", err)
	}
	for _, wf := range workspaceFiles {
		if !filepath.IsLocal(wf.Path) {
			return tmpDir, fmt.Errorf("invalid workspace file path: %s", wf.Path)
		}
		dst := filepath.Join(inputRoot, wf.Path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return tmpDir, fmt.Errorf("failed to create workspace mkdir: %w", err)
		}
		if err := os.WriteFile(dst, []byte(wf.Content), 0o644); err != nil {
			return tmpDir, fmt.Errorf("failed to write workspace file: %w", err)
		}
	}
	codePath := filepath.Join(inputRoot, codeFilename)
	if err := os.WriteFile(codePath, []byte(code), 0o644); err != nil {
		return tmpDir, fmt.Errorf("failed to write code file: %w", err)
	}
	return tmpDir, nil
}

func effectiveTimeout(reqTimeout time.Duration, limits LangLimits) time.Duration {
	if reqTimeout <= 0 {
		return limits.MaxTimeout
	}
	return max(limits.MinTimeout, min(reqTimeout, limits.MaxTimeout))
}

func effectiveMemoryBytes(reqBytes int64, limits LangLimits) int64 {
	if reqBytes <= 0 {
		return limits.MaxMemoryBytes
	}
	return max(limits.MinMemoryBytes, min(reqBytes, limits.MaxMemoryBytes))
}

func getHostConfig(spec RunSpec, ds dockerSpec, inputSrc string) *container.HostConfig {
	hc := &container.HostConfig{
		Resources: container.Resources{
			PidsLimit: new(ds.limits.MaxPids),
			NanoCPUs:  int64(math.Ceil(ds.limits.MaxCPUs * 1e9)),
		},
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			sandboxDir: fmt.Sprintf("size=%d,noexec,mode=1777", ds.limits.WorkspaceSizeBytes),
			"/tmp":     fmt.Sprintf("size=%d,noexec,mode=1777", ds.limits.TmpSizeBytes),
		},
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeBind,
				Source:   inputSrc,
				Target:   inputDir,
				ReadOnly: true,
			},
		},
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": MaxLogConfigFileSize, "max-file": MaxLogConfigFileCount},
		},
	}
	hc.Memory = effectiveMemoryBytes(spec.MemoryBytes, ds.limits)
	hc.MemorySwap = hc.Memory
	if ds.langType == "compiled" {
		hc.Tmpfs[buildDir] = "size=10m,exec,mode=1777,nosuid,nodev"
	}
	return hc
}

func statusForExit(code int64) Status {
	switch code {
	case 0:
		return StatusOK
	case compileFailExitCode:
		return StatusCompileError
	default:
		return StatusRuntimeError
	}
}
