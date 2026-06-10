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

	"github.com/tpsawant027/runboxd/internal/registry"
)

const (
	wrapperPath   = "/wrapper.sh"
	nsjailLogName = "nsjail.log"

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
mount { src: {{.Rootfs | quote}} dst: "/" is_bind: true }
mount { src: {{.TmpDir | quote}} dst: "/input" is_bind: true }
mount { dst: "/sandbox" fstype: "tmpfs" rw: true options: {{.SandboxOpts | quote}} noexec: true nosuid: true nodev: true }
mount { dst: "/tmp" fstype: "tmpfs" rw: true options: {{.TmpOpts | quote}} noexec: true }
exec_bin { path: "/wrapper.sh"{{range .RunCmd}} arg: {{. | quote}}{{end}} }
`),
)

type nsjailSpec struct {
	filename string
	limits   LangLimits
	runCmd   []string
	rootfs   string
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

		spec := langEntry{
			defaultVersion: defaultVersion.Name,
			langType:       entry.Type,
			filename:       entry.Filename,
			limits:         limits,
			versions:       make(map[string]versionSpec, len(entry.Versions)),
		}
		for _, version := range entry.Versions {
			spec.versions[version.Name] = versionSpec{image: version.Image, runCmd: version.RunCmd}
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

	timeout := effectiveTimeout(spec.Timeout, ns.limits)

	cfgPath, err := writeNsjailConfig(ns, tmpDir, timeout)
	if err != nil {
		return runResInternalErr, fmt.Errorf("failed to write nsjail config: %w", err)
	}

	// nsjail's own time_limit is the authority (it tears down the pid ns cleanly); the
	// execCtx outlives it by a grace margin and only fires if nsjail itself hangs.
	execCtx, execCancel := context.WithTimeout(ctx, timeout+nsjailKillGrace)
	defer execCancel()

	cmd := exec.CommandContext(execCtx, s.nsjailPath, "--config", cfgPath)
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}
	stdoutBuf := &limitWriter{n: MaxOutputBytes}
	stderrBuf := &limitWriter{n: MaxOutputBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	startedAt := time.Now()
	err = cmd.Run()
	duration := time.Since(startedAt)

	exitCode := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		exitCode = exitErr.ExitCode()
	}

	// A signal-killed child returns "signal: killed", NOT context.DeadlineExceeded, so a
	// timeout is detected from nsjail's log token (its time_limit firing) with the
	// execCtx deadline as the backstop for a hung nsjail.
	timedOut := nsjailHitTimeLimit(filepath.Join(tmpDir, nsjailLogName)) ||
		errors.Is(execCtx.Err(), context.DeadlineExceeded)
	if timedOut {
		exitCode = -1
	}

	return RunResult{
		Stdout:   stdoutBuf.buf.String(),
		Stderr:   stderrBuf.buf.String(),
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
		filename: entry.filename,
		limits:   entry.limits,
		runCmd:   vs.runCmd,
		rootfs:   filepath.Join(s.rootfsRoot, lang, version),
	}, nil
}

func writeNsjailConfig(ns nsjailSpec, tmpDir string, timeout time.Duration) (string, error) {
	cfgPath := filepath.Join(tmpDir, "nsjail.cfg")
	cfgFile, err := os.Create(cfgPath)
	if err != nil {
		return "", fmt.Errorf("creating nsjail config file: %w", err)
	}
	defer cfgFile.Close()

	cfgData := struct {
		UID         string
		GID         string
		TimeoutSec  int
		MemMB       int64
		MaxPids     int64
		FsizeMB     int
		LogPath     string
		Rootfs      string
		TmpDir      string
		SandboxOpts string
		TmpOpts     string
		RunCmd      []string
	}{
		UID:         nsjailNobodyUID,
		GID:         nsjailNobodyGID,
		TimeoutSec:  int(math.Ceil(timeout.Seconds())),
		MemMB:       ns.limits.MaxMemoryBytes / (1024 * 1024),
		MaxPids:     ns.limits.MaxPids,
		FsizeMB:     nsjailMaxFsizeMB,
		LogPath:     filepath.Join(tmpDir, nsjailLogName),
		Rootfs:      ns.rootfs,
		TmpDir:      tmpDir,
		SandboxOpts: fmt.Sprintf("size=%d", sandboxTmpfsSize),
		TmpOpts:     fmt.Sprintf("size=%d", tmpTmpfsSize),
		RunCmd:      ns.runCmd,
	}

	if err := nsjailCfgTmpl.Execute(cfgFile, cfgData); err != nil {
		return "", fmt.Errorf("rendering nsjail config template: %w", err)
	}

	return cfgPath, nil
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
