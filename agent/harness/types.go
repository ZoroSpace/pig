// pi: src/harness/types.ts — harness capability types.
//
// Not yet ported (arrive with their consumers, placeholders left at their
// source positions below): Result/ok/err/getOrThrow/getOrUndefined/toError
// (TS Result<T, E> collapses to Go (T, error) at each consumer; no runtime
// counterpart is needed so far — the TaggedError half now lives in
// result.go).
//
// Segment deviations (FileSystem):
//   - pi's FileSystem methods return Promise<Result<T, FileError>>; Go
//     methods return (T, error) with *FileError as the concrete error type
//     (same collapse as the session layer's Result handling).
//   - pi's optional abortSignal parameters (bare or inside options objects)
//     map to a leading context.Context on every method, per the
//     AbortSignal → context rule. Callers without a signal pass a non-nil
//     background context; the session layer (whose TS source never forwards
//     an abort signal) does exactly that.
//   - cleanup() keeps its best-effort no-throw contract as a nil error.
//   - pi's `content: string | Uint8Array` on writeFile/appendFile collapses
//     to []byte (callers convert strings).
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// Skill loaded from a `SKILL.md` file or provided by an application (pi:
// Skill).
//
// Name, Description, and FilePath are inserted into the system prompt in an
// XML-formatted block as suggested by agentskills.io. Use
// FormatSkillsForSystemPrompt to generate the spec-compatible system prompt
// block.
type Skill struct {
	// Name is the stable skill name used for lookup and model-visible
	// listings.
	Name string `json:"name"`
	// Description is the short model-visible description of when to use the
	// skill.
	Description string `json:"description"`
	// Content is the full skill instructions.
	Content string `json:"content"`
	// FilePath is the absolute path to the skill file. Used for
	// model-visible location and resolving relative references.
	FilePath string `json:"filePath"`
	// DisableModelInvocation excludes this skill from model-visible skill
	// lists while still allowing explicit application invocation. Pointer:
	// TS optional boolean; nil = pi's `undefined`.
	DisableModelInvocation *bool `json:"disableModelInvocation,omitempty"`
}

// PromptTemplate that can be formatted into a prompt for explicit invocation
// (pi: PromptTemplate).
type PromptTemplate struct {
	// Name is the stable template name used for lookup or application
	// command routing.
	Name string `json:"name"`
	// Description is an optional description for command lists or
	// autocomplete. Pointer: TS optional string; nil = pi's `undefined`.
	Description *string `json:"description,omitempty"`
	// Content is the template content. Argument placeholders are formatted
	// by FormatPromptTemplateInvocation.
	Content string `json:"content"`
}

// AgentHarnessResources are resources made available to explicit invocation
// methods and system-prompt callbacks (pi: AgentHarnessResources<...>).
//
// pi's TSkill/TPromptTemplate generics default to Skill/PromptTemplate and
// have no other instantiation in the codebase; per the generic-collapse rule
// they are inlined as the concrete types.
type AgentHarnessResources struct {
	// PromptTemplates available for explicit invocation.
	PromptTemplates []PromptTemplate `json:"promptTemplates,omitempty"`
	// Skills available to the model and explicit skill invocation.
	Skills []Skill `json:"skills,omitempty"`
}

// AgentHarnessTool is a tool definition used with the harness (pi:
// AgentHarnessTool<TContext, TParameters, TDetails> = Omit<AgentTool,
// "execute"> & { execute }). The generics are dropped (same rule as
// agent.AgentTool, see the agent/types.go note): params arrive as a
// schema-validated map[string]any (pi: Static<TParameters>), details as any
// (pi: TDetails), and the turn context as any (pi: TContext). The Execute
// signature differs from agent.AgentTool by the trailing context parameter,
// resolved for the current turn snapshot.
type AgentHarnessTool struct {
	Name             string
	Label            string
	Description      string
	Parameters       json.RawMessage
	PrepareArguments func(args any) any
	ExecutionMode    agent.ToolExecutionMode
	Execute          func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, context any) (agent.AgentToolResult, error)
}

// AgentHarnessToolContextSource is pi's `TContext | (() => TContext |
// Promise<TContext>)` union. It has no standalone Go type: callers pass
// either a plain context value or a provider function, encoded as the two
// AgentHarnessOptions fields ToolContext / ToolContextProvider (only one may
// be set).

// AgentHarnessToolContextSource is pi's `TContext | (() => TContext |
// Promise<TContext>)` union. It has no standalone Go type: callers pass
// either a plain context value or a provider function, encoded as the two
// AgentHarnessOptions fields ToolContext / ToolContextProvider (only one may
// be set).

// AgentHarnessStreamOptions are curated provider request options owned by
// the harness and snapshotted per turn (pi: AgentHarnessStreamOptions).
type AgentHarnessStreamOptions struct {
	// Transport is the preferred transport forwarded to the stream function.
	// Pointer: TS optional; nil = pi's `undefined`.
	Transport *types.Transport
	// TimeoutMs is the provider request timeout in milliseconds. Pointer:
	// TS optional number.
	TimeoutMs *int64
	// MaxRetries is the maximum provider retry attempts. Pointer: TS
	// optional number.
	MaxRetries *int
	// MaxRetryDelayMs caps provider-requested retry delays in milliseconds.
	// Pointer: TS optional number.
	MaxRetryDelayMs *int64
	// Headers are additional request headers merged with auth and lifecycle
	// headers (pi: Record<string, string>).
	Headers map[string]string
	// Metadata is provider metadata forwarded with requests (pi:
	// SimpleStreamOptions["metadata"]).
	Metadata map[string]any
	// CacheRetention is the provider cache retention hint. Zero value "" =
	// pi's `undefined`, matching types.StreamOptions' convention.
	CacheRetention types.CacheRetention
}

// AgentHarnessStreamOptionsPatch is a partial update of the harness stream
// options (pi: AgentHarnessStreamOptionsPatch = Omit<Partial<...>,
// "headers" | "metadata">). Nil fields keep the current values; pi's omitted
// headers/members are not patchable and therefore absent here.
type AgentHarnessStreamOptionsPatch struct {
	Transport       *types.Transport
	TimeoutMs       *int64
	MaxRetries      *int
	MaxRetryDelayMs *int64
	CacheRetention  *types.CacheRetention
}

// FileKind is the kind of filesystem object as addressed by a FileSystem
// (pi: FileKind). Symlinks are not followed automatically.
type FileKind string

const (
	FileKindFile      FileKind = "file"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// FileErrorCode is the stable, backend-independent file error code vocabulary
// (pi: FileErrorCode).
type FileErrorCode string

const (
	FileCodeAborted      FileErrorCode = "aborted"
	FileCodeNotFound     FileErrorCode = "not_found"
	FileCodePermission   FileErrorCode = "permission_denied"
	FileCodeNotDirectory FileErrorCode = "not_directory"
	FileCodeIsDirectory  FileErrorCode = "is_directory"
	FileCodeInvalid      FileErrorCode = "invalid"
	FileCodeNotSupported FileErrorCode = "not_supported"
	FileCodeUnknown      FileErrorCode = "unknown"
)

// FileError is the error returned by FileSystem file operations (pi:
// FileError class).
type FileError struct {
	// Code is the backend-independent error code.
	Code FileErrorCode
	// Path is the absolute addressed path associated with the failure, when
	// available.
	Path  string
	msg   string
	cause error
}

// NewFileError builds a FileError; path may be empty and cause may be nil.
func NewFileError(code FileErrorCode, message string, path string, cause error) *FileError {
	return &FileError{Code: code, Path: path, msg: message, cause: cause}
}

func (e *FileError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.msg, e.cause)
	}
	return e.msg
}

func (e *FileError) Unwrap() error { return e.cause }

// AsFileError extracts a *FileError from an error chain (pi: property access
// on Result<T, FileError>.error, whose code field drives the branching in
// the skill/template loaders).
func AsFileError(err error) (*FileError, bool) {
	var fileErr *FileError
	if err == nil {
		return nil, false
	}
	if errors.As(err, &fileErr) {
		return fileErr, true
	}
	return nil, false
}

// ExecutionErrorCode is the stable, backend-independent execution error code
// vocabulary returned by ExecutionEnv.Exec (pi: ExecutionErrorCode).
type ExecutionErrorCode string

const (
	ExecCodeAborted          ExecutionErrorCode = "aborted"
	ExecCodeTimeout          ExecutionErrorCode = "timeout"
	ExecCodeShellUnavailable ExecutionErrorCode = "shell_unavailable"
	ExecCodeSpawnError       ExecutionErrorCode = "spawn_error"
	ExecCodeCallbackError    ExecutionErrorCode = "callback_error"
	ExecCodeUnknown          ExecutionErrorCode = "unknown"
)

// ExecutionError is the error returned by ExecutionEnv.Exec (pi:
// ExecutionError class).
type ExecutionError struct {
	// Code is the backend-independent error code.
	Code  ExecutionErrorCode
	msg   string
	cause error
}

// NewExecutionError builds an ExecutionError; cause may be nil.
func NewExecutionError(code ExecutionErrorCode, message string, cause error) *ExecutionError {
	return &ExecutionError{Code: code, msg: message, cause: cause}
}

func (e *ExecutionError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.msg, e.cause)
	}
	return e.msg
}

func (e *ExecutionError) Unwrap() error { return e.cause }

// CompactionErrorCode is the stable compaction error code vocabulary
// returned by compaction helpers (pi: CompactionErrorCode).
type CompactionErrorCode string

const (
	CompactionCodeAborted             CompactionErrorCode = "aborted"
	CompactionCodeSummarizationFailed CompactionErrorCode = "summarization_failed"
)

// CompactionError is the error returned by compaction helpers (pi:
// CompactionError class).
type CompactionError struct {
	// Code is the backend-independent error code.
	Code  CompactionErrorCode
	msg   string
	cause error
}

// NewCompactionError builds a CompactionError; cause may be nil.
func NewCompactionError(code CompactionErrorCode, message string, cause error) *CompactionError {
	return &CompactionError{Code: code, msg: message, cause: cause}
}

func (e *CompactionError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.msg, e.cause)
	}
	return e.msg
}

func (e *CompactionError) Unwrap() error { return e.cause }

// BranchSummaryErrorCode is the stable branch-summary error code vocabulary
// returned by branch summarization helpers (pi: BranchSummaryErrorCode).
type BranchSummaryErrorCode string

const (
	BranchSummaryCodeAborted             BranchSummaryErrorCode = "aborted"
	BranchSummaryCodeSummarizationFailed BranchSummaryErrorCode = "summarization_failed"
)

// BranchSummaryError is the error returned by branch summarization helpers
// (pi: BranchSummaryError class).
type BranchSummaryError struct {
	// Code is the backend-independent error code.
	Code  BranchSummaryErrorCode
	msg   string
	cause error
}

// NewBranchSummaryError builds a BranchSummaryError; cause may be nil.
func NewBranchSummaryError(code BranchSummaryErrorCode, message string, cause error) *BranchSummaryError {
	return &BranchSummaryError{Code: code, msg: message, cause: cause}
}

func (e *BranchSummaryError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.msg, e.cause)
	}
	return e.msg
}

func (e *BranchSummaryError) Unwrap() error { return e.cause }

// FileInfo is the metadata for one filesystem object in a FileSystem (pi:
// FileInfo).
type FileInfo struct {
	// Name is the basename of Path.
	Name string `json:"name"`
	// Path is the absolute, syntactically normalized addressed path.
	// Symlinks are not followed.
	Path string `json:"path"`
	// Kind is the object kind; symlink targets are not followed.
	Kind FileKind `json:"kind"`
	// Size is the size in bytes.
	Size int64 `json:"size"`
	// MtimeMs is the modification time as milliseconds since Unix epoch.
	MtimeMs int64 `json:"mtimeMs"`
}

// FileSystem is the filesystem capability used by the harness (pi:
// FileSystem).
//
// Paths passed to methods may be absolute or relative to Cwd. Paths returned
// by file operations are addressed paths in the filesystem namespace, but are
// not canonicalized through symlinks unless returned by CanonicalPath.
//
// Operation methods must never panic; all filesystem failures, including
// unexpected backend failures, must be encoded in the returned error as a
// *FileError. Implementations must preserve this invariant.
type FileSystem interface {
	// Cwd is the current working directory for relative paths.
	Cwd() string

	// AbsolutePath returns an absolute addressed path without requiring it to
	// exist and without resolving symlinks.
	AbsolutePath(ctx context.Context, path string) (string, error)
	// JoinPath joins path segments in the filesystem namespace without
	// requiring the result to exist.
	JoinPath(ctx context.Context, parts []string) (string, error)
	// ReadTextFile reads a UTF-8 text file.
	ReadTextFile(ctx context.Context, path string) (string, error)
	// ReadTextLines reads UTF-8 text lines. Implementations should stop once
	// maxLines lines have been read.
	ReadTextLines(ctx context.Context, path string, maxLines *int) ([]string, error)
	// ReadBinaryFile reads a binary file.
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	// WriteFile creates or overwrites a file, creating parent directories
	// when supported (pi: writeFile).
	WriteFile(ctx context.Context, path string, content []byte) error
	// AppendFile creates or appends to a file, creating parent directories
	// when supported (pi: appendFile).
	AppendFile(ctx context.Context, path string, content []byte) error
	// RenameFile atomically renames a file, replacing the destination when it
	// exists. Does not copy across filesystems.
	RenameFile(ctx context.Context, sourcePath string, destinationPath string) error
	// FileInfo returns metadata for the addressed path without following
	// symlinks.
	FileInfo(ctx context.Context, path string) (FileInfo, error)
	// ListDir lists direct children of a directory without following
	// symlinks.
	ListDir(ctx context.Context, path string) ([]FileInfo, error)
	// CanonicalPath returns the canonical path for an existing path,
	// resolving symlinks where supported.
	CanonicalPath(ctx context.Context, path string) (string, error)
	// Exists returns false for missing paths. Other errors, such as
	// permission failures, return a *FileError.
	Exists(ctx context.Context, path string) (bool, error)
	// CreateDir creates a directory. Defaults: recursive true (pi:
	// createDir's options.recursive).
	CreateDir(ctx context.Context, path string, recursive *bool) error
	// Remove removes a file or directory. Defaults: recursive false, force
	// false (pi: remove's options).
	Remove(ctx context.Context, path string, recursive *bool, force *bool) error
	// CreateTempDir creates a temporary directory and returns its absolute
	// path. Defaults: prefix "tmp-".
	CreateTempDir(ctx context.Context, prefix *string) (string, error)
	// CreateTempFile creates a temporary file and returns its absolute path.
	// Defaults: prefix "", suffix "".
	CreateTempFile(ctx context.Context, prefix *string, suffix *string) (string, error)

	// Cleanup releases filesystem resources. Must be best-effort and must
	// not fail session flow (pi: "must not throw or reject").
	Cleanup() error
}

// ShellExecResult is the success payload of Shell.Exec (pi: the anonymous
// `{ stdout, stderr, exitCode }` result).
type ShellExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ShellExecOptions are options for Shell.Exec (pi: ShellExecOptions).
type ShellExecOptions struct {
	// Cwd is the working directory for the command. Relative paths are
	// resolved against ExecutionEnv.Cwd; empty defaults to it.
	Cwd string
	// Env holds environment variables for the command. Values override
	// inherited defaults when InheritEnv is true.
	Env map[string]string
	// InheritEnv controls whether the execution environment's default
	// variables are inherited. Pointer because pi's default is true; nil
	// means that default.
	InheritEnv *bool
	// Timeout is the timeout in seconds. Implementations should return a
	// timeout error when the command exceeds this duration. nil means no
	// timeout (pi: number seconds).
	Timeout *float64
	// OnStdout is called with stdout chunks as they are produced.
	OnStdout func(chunk string)
	// OnStderr is called with stderr chunks as they are produced.
	OnStderr func(chunk string)
	// pi's abortSignal is intentionally absent: cancellation travels as the
	// context.Context passed to Exec alongside these options.
}

// Shell is the shell execution capability used by the harness (pi:
// interface Shell). Exec failures return a *ExecutionError as the concrete
// error (pi: Result<..., ExecutionError> collapse, see header note).
type Shell interface {
	// Exec executes a shell command in FileSystem.Cwd unless options.Cwd is
	// provided. ctx carries the caller's cancellation (pi: options.
	// abortSignal).
	Exec(ctx context.Context, command string, options *ShellExecOptions) (*ShellExecResult, error)
	// Cleanup releases shell resources. Must be best-effort and must not
	// fail session flow (pi: "must not throw or reject").
	Cleanup() error
}

// ExecutionEnv is the filesystem and process execution environment used by
// the harness (pi: interface ExecutionEnv extends FileSystem, Shell).
type ExecutionEnv interface {
	FileSystem
	Shell
}

// CompactionSettings holds compaction thresholds and retention settings (pi:
// CompactionSettings from compaction/compaction.ts).
//
// Defined here (package harness) instead of the compaction subpackage to
// break an import cycle: pi's agent-harness.ts lives in the harness root and
// consumes CompactionSettings, while pig's compaction package imports
// harness for the message helpers and error types. The compaction package
// re-exports the alias, keeping both pi import paths working.
type CompactionSettings struct {
	// Enabled enables automatic compaction decisions.
	Enabled bool
	// ReserveTokens is the token budget reserved for summary prompt and
	// output.
	ReserveTokens int64
	// KeepRecentTokens is the approximate recent-context tokens to keep
	// after compaction.
	KeepRecentTokens int64
}

// DefaultCompactionSettings are the default compaction settings used by the
// harness (pi: DEFAULT_COMPACTION_SETTINGS).
var DefaultCompactionSettings = CompactionSettings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}
