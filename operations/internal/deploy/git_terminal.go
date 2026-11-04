package deploy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func gitTerminalBlock(binary string) []byte {
	dir := gitQuote(filepath.Dir(binary))
	return []byte("# >>> streamfusion-ops git >>>\n" +
		"if [ -x " + gitQuote(binary) + " ]; then\n  case \":$PATH:\" in *:" + dir + ":*) ;; *) PATH=" + dir + ":\"$PATH\"; export PATH ;; esac\nfi\n" +
		"# <<< streamfusion-ops git <<<\n")
}

func gitTerminalFiles(g GitRepositoryRequest, a *user.User) (nodeTerminalPlan, error) {
	plan := nodeTerminalPlan{Files: []nodeTerminalFile{}}
	if g.GitBinary == "/usr/bin/git" {
		if resolved, err := filepath.EvalSymlinks("/usr/bin/git"); err == nil && secureAncestors(resolved) == nil {
			if info, err := os.Stat(resolved); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				return plan, nil
			}
		}
	}
	if strings.Contains(g.GitBinary, ":") {
		return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
	}
	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil || len(passwd) > 1<<20 {
		return plan, errors.New("GIT_ACCOUNT_CHANGED")
	}
	bash := false
	for _, line := range strings.Split(string(passwd), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) == 7 && fields[0] == a.Username && fields[2] == a.Uid && fields[5] == a.HomeDir {
			bash = fields[6] == "/bin/bash" || fields[6] == "/usr/bin/bash"
		}
	}
	if !bash {
		return plan, errors.New("GIT_TERMINAL_UNSUPPORTED")
	}
	login := ".profile"
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if _, e := os.Lstat(filepath.Join(a.HomeDir, name)); e == nil {
			login = name
			break
		} else if !os.IsNotExist(e) {
			return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
		}
	}
	root, err := os.OpenRoot(a.HomeDir)
	if err != nil {
		return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
	}
	defer root.Close()
	uid, _ := strconv.Atoi(a.Uid)
	block := gitTerminalBlock(g.GitBinary)
	for _, name := range []string{".bashrc", login} {
		f := nodeTerminalFile{Path: filepath.Join(a.HomeDir, name), Mode: 0600}
		raw := []byte(nil)
		st, err := root.Lstat(name)
		if err == nil {
			if gitOwnedPath(f.Path, uid, false) != nil || st.Size() > 128<<10 {
				return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
			}
			input, err := root.Open(name)
			if err != nil {
				return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
			}
			opened, e := input.Stat()
			if e == nil && os.SameFile(st, opened) {
				raw, e = io.ReadAll(io.LimitReader(input, (128<<10)+1))
			} else {
				e = errors.New("GIT_TERMINAL_PATH_UNSAFE")
			}
			input.Close()
			if e != nil || len(raw) > 128<<10 {
				return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
			}
			f.BeforeHash, f.Mode = nodeBytesSHA(raw), uint32(st.Mode().Perm())
		} else if !os.IsNotExist(err) {
			return plan, errors.New("GIT_TERMINAL_PATH_UNSAFE")
		}
		f.Content = raw
		if bytes.Count(raw, block) != 1 {
			if bytes.Contains(raw, []byte("streamfusion-ops git")) {
				return plan, errors.New("GIT_TERMINAL_CONFLICT")
			}
			if len(raw) > 0 && raw[len(raw)-1] != '\n' {
				f.Content = append(f.Content, '\n')
			}
			f.Content = append(f.Content, block...)
		} else if bytes.Contains(bytes.Replace(raw, block, nil, 1), []byte("streamfusion-ops git")) {
			return plan, errors.New("GIT_TERMINAL_CONFLICT")
		}
		f.AfterHash = nodeBytesSHA(f.Content)
		plan.Files = append(plan.Files, f)
	}
	return plan, nil
}

func (x *executor) connectGitTerminal(ctx context.Context, g GitRepositoryRequest, a *user.User, job, dir string) error {
	plan, err := gitTerminalFiles(g, a)
	if err != nil {
		return err
	}
	if g.ExpectedTerminalHash == "" || Digest(plan) != g.ExpectedTerminalHash {
		return errors.New("GIT_TERMINAL_CHANGED")
	}
	if err = atomicJSON(filepath.Join(dir, "git-terminal.json"), plan); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("/var/tmp", "sfops-git-terminal-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	uid, _ := strconv.Atoi(a.Uid)
	gid, _ := strconv.Atoi(a.Gid)
	if err = os.Chown(stage, uid, gid); err != nil {
		return err
	}
	if err = os.Chmod(stage, 0700); err != nil {
		return err
	}
	for index, f := range plan.Files {
		if f.BeforeHash == f.AfterHash {
			continue
		}
		file := filepath.Join(stage, strconv.Itoa(index))
		if err = os.WriteFile(file, f.Content, 0400); err != nil {
			return err
		}
		if err = os.Chmod(file, 0400); err != nil {
			return err
		}
		if err = os.Chown(file, uid, gid); err != nil {
			return err
		}
		backup := filepath.Join(a.HomeDir, ".local/share/streamfusion-ops/git-terminal/backups", job)
		args := []string{"-u", a.Username, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + a.HomeDir, "LC_ALL=C", "/usr/bin/bash", "--noprofile", "--norc", "-c", nodeTerminalScript, "sfops-git-terminal", f.Path, f.BeforeHash, f.AfterHash, strconv.FormatUint(uint64(f.Mode), 8), file, backup, a.Uid}
		if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
			return errors.New("GIT_TERMINAL_RESULT_UNCONFIRMED")
		}
	}
	if !gitTerminalPlanApplied(g, a, dir) {
		return errors.New("GIT_TERMINAL_RESULT_UNCONFIRMED")
	}
	return nil
}

func gitTerminalPlanApplied(g GitRepositoryRequest, a *user.User, dir string) bool {
	var expected nodeTerminalPlan
	if readJSON(filepath.Join(dir, "git-terminal.json"), &expected) != nil {
		return false
	}
	actual, err := gitTerminalFiles(g, a)
	if err != nil || len(expected.Files) != len(actual.Files) {
		return false
	}
	for index, f := range expected.Files {
		actualFile := actual.Files[index]
		if f.Path != actualFile.Path || f.Mode != actualFile.Mode || f.AfterHash != actualFile.BeforeHash || actualFile.AfterHash != actualFile.BeforeHash {
			return false
		}
	}
	return true
}
