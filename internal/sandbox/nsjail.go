package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/tpsawant027/runboxd/internal/imagespec"
	"github.com/tpsawant027/runboxd/internal/registry"
)

const (
	wrapperPath = "/wrapper.sh"

	nsjailBuildCfgName = "nsjail-build.cfg"
	nsjailRunCfgName   = "nsjail-run.cfg"
	nsjailBuildLogName = "nsjail-build.log"
	nsjailRunLogName   = "nsjail-run.log"

	nsjailNobodyUID = "65534"
	nsjailNobodyGID = "65534"

	sandboxTmpfsSize = 10 * 1024 * 1024 // 10 MiB
	tmpTmpfsSize     = 5 * 1024 * 1024  // 5 MiB

	nsjailKillGrace = 2 * time.Second

	nsjailMaxFsizeMB = 16
)

var nsjailCfgTmpl = template.Must(
	template.New("nsjail").Funcs(template.FuncMap{
		"quote": strconv.Quote,
	}).Parse(`mode: ONCE
cwd: "/sandbox"
mount_proc: true
uidmap { inside_id: {{.UID | quote}} outside_id: "" }
gidmap { inside_id: {{.GID | quote}} outside_id: "" }
time_limit: {{.TimeoutSec}}
rlimit_as: {{.MemMB}}
rlimit_nproc: {{.MaxPids}}
rlimit_nproc_type: VALUE
rlimit_fsize: {{.FsizeMB}}
log_file: {{.LogPath | quote}}
{{range $k, $v := .Env}}envar: {{printf "%s=%s" $k $v | quote}}
{{end}}
mount { src: {{.Rootfs | quote}} dst: "/" is_bind: true }
mount { src: {{.InputDir | quote}} dst: "/input" is_bind: true }
{{if .BuildDir}}mount { src: {{.BuildDir | quote}} dst: "/build" is_bind: true rw: true noexec: false nosuid: true nodev: true }
{{end}}
mount { dst: "/sandbox" fstype: "tmpfs" rw: true options: {{.SandboxOpts | quote}} noexec: true nosuid: true nodev: true }
mount { dst: "/tmp" fstype: "tmpfs" rw: true options: {{.TmpOpts | quote}} noexec: true }
exec_bin { path: "/wrapper.sh"{{range .Cmd}} arg: {{. | quote}}{{end}} }
`),
)

type nsjailCfgData struct {
	UID         string
	GID         string
	TimeoutSec  int
	MemMB       int64
	MaxPids     int64
	FsizeMB     int
	LogPath     string
	Rootfs      string
	InputDir    string
	BuildDir    string
	SandboxOpts string
	TmpOpts     string
	Cmd         []string
	Env         map[string]string
}

type nsjailSpec struct {
	langType      string
	filename      string
	rootfs        string
	limits        LangLimits
	compileLimits LangCompileLimits
	runCmd        []string
	buildCmd      []string
	env           map[string]string
}

type NsjailSandbox struct {
	nsjailPath string
	rootfsRoot string
	specs      map[string]langEntry
	logger     *slog.Logger
}

var (
	_ Sandbox  = (*NsjailSandbox)(nil)
	_ Informer = (*NsjailSandbox)(nil)
	_ Pinger   = (*NsjailSandbox)(nil)
)

func NewNsjailSandbox(registryPath, nsjailPath string, rootfsRoot string, logger *slog.Logger) (*NsjailSandbox, error) {
	if nsjailPath == "" {
		var err error
		nsjailPath, err = exec.LookPath("nsjail")
		if err != nil {
			return nil, fmt.Errorf("nsjail binary not found in PATH: %w", err)
		}
	}

	// nsjail resolves bind-mount source paths at mount time, after it has set up its
	// own mount namespace — a relative rootfs source is not reliably resolvable there
	// and fails with "Failed to mount mandatory point: '/'". Pin it to an absolute path.
	rootfsRoot, err := filepath.Abs(rootfsRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving rootfs root %q to absolute path: %w", rootfsRoot, err)
	}

	registry, err := registry.Load(registryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load registry: %w", err)
	}

	specs := make(map[string]langEntry, len(registry.Languages))

	for _, entry := range registry.Languages {
		defaultVersion, ok := entry.Versions[entry.DefaultVersion]
		if !ok {
			return nil, fmt.Errorf("default version %q for language %q not found in registry", entry.DefaultVersion, entry.Name)
		}

		limits := resolveLangLimits(entry.Limits)
		if err := validateLangLimits(limits); err != nil {
			return nil, fmt.Errorf("invalid limits for language %q: %w", entry.Name, err)
		}

		compileLimits := resolveLangCompileLimits(entry.CompileLimits)
		if err := validateLangCompileLimits(compileLimits); err != nil {
			return nil, fmt.Errorf("invalid compile limits for language %q: %w", entry.Name, err)
		}

		spec := langEntry{
			defaultVersion: defaultVersion.Name,
			langType:       entry.Type,
			filename:       entry.Filename,
			env:            entry.Env,
			limits:         limits,
			compileLimits:  compileLimits,
			versions:       make(map[string]versionSpec, len(entry.Versions)),
		}
		for _, version := range entry.Versions {
			spec.versions[version.Name] = versionSpec{image: version.Image, runCmd: version.RunCmd, buildCmd: version.BuildCmd}
			versionRoot := filepath.Join(rootfsRoot, entry.Name, version.Name)
			if _, err := os.Stat(versionRoot); err != nil {
				return nil, fmt.Errorf("rootfs for language %q version %q not found at %q: %w", entry.Name, version.Name, versionRoot, err)
			}
			for _, requiredFile := range []string{wrapperPath, "/sandbox", "/input", "/tmp"} {
				if _, err := os.Stat(filepath.Join(versionRoot, requiredFile)); err != nil {
					return nil, fmt.Errorf("required file %q for language %q version %q not found in rootfs: %w", requiredFile, entry.Name, version.Name, err)
				}
			}

		}
		specs[entry.Name] = spec

	}

	return &NsjailSandbox{
		nsjailPath: nsjailPath,
		rootfsRoot: rootfsRoot,
		specs:      specs,
		logger:     logger,
	}, nil
}

func (s *NsjailSandbox) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	ns, err := s.lookupSpec(spec.Language, spec.Version)
	if err != nil {
		return RunResult{}, err
	}

	runResInternalErr := RunResult{Status: StatusInternalError}

	tmpDir, err := setupWorkspace("runboxd-*", spec.Code, ns.filename, spec.WorkspaceFiles)
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to setup workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if ns.langType == "compiled" && len(ns.buildCmd) > 0 {
		if err := os.MkdirAll(filepath.Join(tmpDir, buildDir), 0o755); err != nil {
			return runResInternalErr, fmt.Errorf("failed to create build dir: %w", err)
		}

		buildTimeout := ns.compileLimits.Timeout
		buildCfgPath, err := writeNsjailConfig(nsjailBuildCfgName, ns, tmpDir, buildTimeout, "build")
		if err != nil {
			return runResInternalErr, fmt.Errorf("failed to write nsjail build config: %w", err)
		}
		buildCtx, buildCancel := context.WithTimeout(ctx, buildTimeout+nsjailKillGrace)
		defer buildCancel()

		buildOut := &limitWriter{n: MaxOutputBytes}
		buildErr := &limitWriter{n: MaxOutputBytes}
		buildCmd := exec.CommandContext(buildCtx, s.nsjailPath, "--config", buildCfgPath)
		buildCmd.Stdout = buildOut
		buildCmd.Stderr = buildErr

		runErr := buildCmd.Run()
		exitCode := 0
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			exitCode = exitErr.ExitCode()
		} else if runErr != nil {
			return runResInternalErr, fmt.Errorf("nsjail build invocation failed: %w", runErr)
		}
		timedOut := nsjailHitTimeLimit(filepath.Join(tmpDir, nsjailBuildLogName)) ||
			errors.Is(buildCtx.Err(), context.DeadlineExceeded)

		if timedOut {
			return RunResult{Stdout: buildOut.String(), Stderr: buildErr.String(), ExitCode: -1, Status: StatusTimeout}, nil
		}
		if exitCode != 0 {
			return RunResult{Stdout: buildOut.String(), Stderr: buildErr.String(), ExitCode: exitCode, Status: StatusCompileError}, nil
		}
	}

	runTimeout := effectiveTimeout(spec.Timeout, ns.limits)

	stdoutBuf := &limitWriter{n: MaxOutputBytes}
	stderrBuf := &limitWriter{n: MaxOutputBytes}

	runCfgPath, err := writeNsjailConfig(nsjailRunCfgName, ns, tmpDir, runTimeout, "run")
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to write nsjail run config: %w", err)
	}

	// nsjail's own time_limit is the authority (it tears down the pid ns cleanly); the
	// runCtx outlives it by a grace margin and only fires if nsjail itself hangs.
	runCtx, execCancel := context.WithTimeout(ctx, runTimeout+nsjailKillGrace)
	defer execCancel()

	runCmd := exec.CommandContext(runCtx, s.nsjailPath, "--config", runCfgPath)
	if spec.Stdin != "" {
		runCmd.Stdin = strings.NewReader(spec.Stdin)
	}
	runCmd.Stdout = stdoutBuf
	runCmd.Stderr = stderrBuf

	startedAt := time.Now()
	err = runCmd.Run()
	duration := time.Since(startedAt)

	exitCode := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		exitCode = exitErr.ExitCode()
	}

	// A signal-killed child returns "signal: killed", NOT context.DeadlineExceeded, so a
	// timeout is detected from nsjail's log token (its time_limit firing) with the
	// execCtx deadline as the backstop for a hung nsjail.
	timedOut := nsjailHitTimeLimit(filepath.Join(tmpDir, nsjailRunLogName)) ||
		errors.Is(runCtx.Err(), context.DeadlineExceeded)
	if timedOut {
		exitCode = -1
	}

	return RunResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
		Status:   statusForNsjailExit(exitCode, timedOut),
		Duration: duration,
	}, nil
}

func (s *NsjailSandbox) lookupSpec(lang, version string) (nsjailSpec, error) {
	entry, ok := s.specs[lang]
	if !ok {
		return nsjailSpec{}, ErrUnsupportedLanguage
	}
	if version == "" {
		version = entry.defaultVersion
	}
	vs, ok := entry.versions[version]
	if !ok {
		return nsjailSpec{}, ErrUnsupportedVersion
	}
	return nsjailSpec{
		langType:      entry.langType,
		filename:      entry.filename,
		rootfs:        filepath.Join(s.rootfsRoot, lang, version),
		limits:        entry.limits,
		compileLimits: entry.compileLimits,
		runCmd:        vs.runCmd,
		buildCmd:      vs.buildCmd,
		env:           entry.env,
	}, nil
}

func writeNsjailConfig(cfgFilename string, ns nsjailSpec, tmpDir string, timeout time.Duration, stage string) (string, error) {
	cfgPath := filepath.Join(tmpDir, cfgFilename)
	cfgFile, err := os.Create(cfgPath)
	if err != nil {
		return "", fmt.Errorf("creating nsjail config file: %w", err)
	}
	defer cfgFile.Close()

	cfgData := nsjailCfgData{
		UID:         nsjailNobodyUID,
		GID:         nsjailNobodyGID,
		TimeoutSec:  int(math.Ceil(timeout.Seconds())),
		MemMB:       ns.limits.MaxMemoryBytes / (1024 * 1024),
		MaxPids:     ns.limits.MaxPids,
		FsizeMB:     nsjailMaxFsizeMB,
		LogPath:     filepath.Join(tmpDir, nsjailRunLogName),
		Rootfs:      ns.rootfs,
		InputDir:    filepath.Join(tmpDir, inputDir),
		SandboxOpts: fmt.Sprintf("size=%d", sandboxTmpfsSize),
		TmpOpts:     fmt.Sprintf("size=%d", tmpTmpfsSize),
		Cmd:         ns.runCmd,
		Env:         ns.env,
	}

	if ns.langType == "compiled" {
		cfgData.BuildDir = filepath.Join(tmpDir, buildDir)
	}

	if stage == "build" {
		cfgData.TimeoutSec = int(math.Ceil(ns.compileLimits.Timeout.Seconds()))
		cfgData.MemMB = ns.compileLimits.MemoryBytes / (1024 * 1024)
		cfgData.MaxPids = ns.compileLimits.MaxPids
		cfgData.LogPath = filepath.Join(tmpDir, nsjailBuildLogName)
		cfgData.Cmd = ns.buildCmd
	}

	if err := nsjailCfgTmpl.Execute(cfgFile, cfgData); err != nil {
		return "", fmt.Errorf("rendering nsjail config template: %w", err)
	}

	return cfgPath, nil
}

func resolveLangCompileLimits(l imagespec.CompileLimits) LangCompileLimits {
	return LangCompileLimits{
		MemoryBytes: valueWithDefault(int64(l.MemoryMiB)*1024*1024, MaxMemoryBytes),
		Timeout:     valueWithDefault(time.Duration(l.TimeoutSeconds)*time.Second, MaxTimeout),
		MaxPids:     valueWithDefault(int64(l.MaxPids), MaxPids),
	}
}

func validateLangCompileLimits(limits LangCompileLimits) error {
	if limits.Timeout < 0 {
		return fmt.Errorf("compile timeout must be non-negative")
	}
	if limits.MemoryBytes < 0 {
		return fmt.Errorf("compile memory limit must be non-negative")
	}
	if limits.MaxPids < 1 {
		return fmt.Errorf("compile MaxPids must be at least 1")
	}
	return nil
}

func statusForNsjailExit(code int, timedOut bool) Status {
	if timedOut {
		return StatusTimeout
	}
	if code == 0 {
		return StatusOK
	}
	return StatusRuntimeError
}

// nsjailHitTimeLimit reports whether nsjail killed the child for exceeding its
// time_limit, read from the log file it wrote.
func nsjailHitTimeLimit(logPath string) bool {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("run time >= time limit"))
}

func (s *NsjailSandbox) LangSpec(language, version string) (LangSpec, error) {
	ds, err := s.lookupSpec(language, version)
	if err != nil {
		return LangSpec{}, err
	}
	return LangSpec{
		Filename: ds.filename,
		Limits:   ds.limits,
	}, nil
}

func (s *NsjailSandbox) Info(ctx context.Context) (SandboxInfo, error) {
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

func (s *NsjailSandbox) Ping(ctx context.Context) error {
	if _, err := os.Stat(s.nsjailPath); err != nil {
		return fmt.Errorf("nsjail binary unavailable: %w", err)
	}
	if _, err := os.Stat(s.rootfsRoot); err != nil {
		return fmt.Errorf("rootfs root unavailable: %w", err)
	}
	return nil
}

func (s *NsjailSandbox) Close() error { return nil }
