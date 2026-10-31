package tools

import (
	"context"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/tjcoder-labs/cli/internal/workspace"
)

// workspaceProp is the optional "workspace" parameter every file and
// shell tool accepts. Paths may also be written as name:relative/path.
func workspaceProp() map[string]any {
	return stringProp("Optional workspace name (see [workspaces]); defaults to the active workspace. A path may also be prefixed as name:path.")
}

// workspaces returns the session's set, or a one-entry set for the
// plain WorkspaceRoot (headless and tests).
func (e ExecEnv) workspaces() *workspace.Set {
	if e.Workspaces != nil {
		return e.Workspaces
	}
	return workspace.NewSet(e.WorkspaceRoot)
}

// target resolves a tool's (workspace, path) arguments to a workspace
// and a contained absolute backend path.
func (e ExecEnv) target(ctx context.Context, wsName, p string) (*workspace.Workspace, string, error) {
	return e.workspaces().Target(ctx, wsName, p)
}

// shown formats p for tool results: the plain path locally, host:path
// for SSH workspaces.
func shown(w *workspace.Workspace, p string) string {
	if w.Kind == workspace.KindSSH {
		return w.Host + ":" + p
	}
	return p
}

// resolveInWorkspace resolves p against root and refuses any path that
// escapes the workspace root, via "..", an absolute path outside root,
// or a symlink pointing outside it. This is the confinement boundary
// for the local file tools: without it, a prompt-injected model could
// read or overwrite arbitrary files such as ~/.ssh/id_rsa.
func resolveInWorkspace(root, p string) (string, error) {
	return workspace.NewLocal(root, "").Resolve(context.Background(), p)
}

// pickDir picks the workspace for a command and its working directory.
// Unlike file paths, a command cwd is not confined to the root (the
// command itself could cd anywhere); relative paths are under the root.
func (env ExecEnv) pickDir(wsName, p string) (*workspace.Workspace, string, error) {
	ws, rest, err := env.workspaces().Pick(wsName, p)
	if err != nil {
		return nil, "", err
	}
	if rest == "" {
		if ws.Kind == workspace.KindSSH {
			return ws, ws.Root, nil
		}
		return ws, filepath.Clean(ws.Root), nil
	}
	if ws.Kind == workspace.KindSSH {
		if strings.HasPrefix(rest, "/") {
			return ws, path.Clean(rest), nil
		}
		return ws, path.Join(ws.Root, rest), nil
	}
	return ws, resolvePath(ws.Root, rest), nil
}

// shellIn builds `bash -lc script` in dir for the workspace: a local
// process, or `cd dir && script` over ssh.
func shellIn(ctx context.Context, ws *workspace.Workspace, dir, script string) (*exec.Cmd, error) {
	if ws.Kind == workspace.KindLocal {
		cmd := exec.CommandContext(ctx, "bash", "-lc", script)
		cmd.Dir = dir
		return cmd, nil
	}
	return ws.FS.Command(ctx, dir, script, nil)
}
