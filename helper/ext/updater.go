package ext

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"azml-go/config"
)

// runCmd executes a command and returns its trimmed stdout, or error
func runCmd(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// DefaultUpstreamBranch: the branch this code updates from when UPSTREAM_BRANCH is not set.
const DefaultUpstreamBranch = "new"

// binTagPrefix: GitHub Actions (.github/workflows/build.yml) builds every push of a branch and
// puts the binary on the release "bin-<branch>" (azml-go-linux-amd64.gz + commit.txt).
const binTagPrefix = "bin-"

var ghRepoRe = regexp.MustCompile(`github\.com[/:]([^/]+)/([^/\s]+?)(?:\.git)?/?$`)

// pullAndBuild: like update.py of the Python bot. Fetches UPSTREAM_REPO / UPSTREAM_BRANCH and,
// when there is a newer commit, puts its binary in place of the running one. The binary comes
// ready from the GitHub release (built by GitHub Actions): compiling on a small dyno (512MB)
// runs out of memory. Only outside Heroku, without a release, it compiles with go build.
// The code on disk moves to the new commit only together with its binary, so a failed update
// is tried again next time. rebuilt is true only when a new binary was written to exePath.
func pullAndBuild(ctx context.Context, binWait time.Duration, onStatus func(status string)) (exePath string, rebuilt bool, err error) {
	cfg := config.Get()

	repo := strings.TrimSpace(cfg.UpstreamRepo)
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("UPSTREAM_REPO"))
	}

	branch := strings.TrimSpace(cfg.UpstreamBranch)
	if branch == "" {
		branch = strings.TrimSpace(os.Getenv("UPSTREAM_BRANCH"))
	}
	if branch == "" {
		branch = DefaultUpstreamBranch
	}

	exePath, eErr := os.Executable()
	if eErr != nil || exePath == "" {
		exePath = "azml-go"
	}

	gitPath, gErr := exec.LookPath("git")
	hasGit := gErr == nil && gitPath != ""

	// If no explicit UPSTREAM_REPO, check if current git directory has origin
	if repo == "" && hasGit {
		if origin, oErr := runCmd(ctx, "", "git", "config", "--get", "remote.origin.url"); oErr == nil && origin != "" {
			repo = origin
		}
	}
	if repo == "" {
		onStatus("• <i>No UPSTREAM_REPO configured. Performing direct binary restart...</i>")
		return exePath, false, nil
	}
	if !hasGit {
		onStatus("⚠️ <i>Git not found in PATH. Skipping code pull and restarting current binary...</i>")
		return exePath, false, nil
	}

	workDir, _ := os.Getwd()
	fresh := false
	if _, statErr := os.Stat(filepath.Join(workDir, ".git")); os.IsNotExist(statErr) {
		fresh = true
		onStatus(fmt.Sprintf("• <i>Initializing Git and cloning <code>%s</code> (%s)...</i>", repo, branch))
		_, _ = runCmd(ctx, workDir, "git", "init", "-q")
		_, _ = runCmd(ctx, workDir, "git", "config", "user.email", "bot@azml.local")
		_, _ = runCmd(ctx, workDir, "git", "config", "user.name", "AZML-Go")
		// the Dockerfile runs chmod -R 777: modes are not a change
		_, _ = runCmd(ctx, workDir, "git", "config", "core.fileMode", "false")
		_, _ = runCmd(ctx, workDir, "git", "remote", "add", "origin", repo)
	} else {
		onStatus(fmt.Sprintf("• <i>Checking for updates from <code>%s</code> (%s)...</i>", repo, branch))
		_, _ = runCmd(ctx, workDir, "git", "remote", "set-url", "origin", repo)
	}
	if _, fErr := runCmd(ctx, workDir, "git", "fetch", "-q", "origin", branch); fErr != nil {
		log.Printf("[Updater] git fetch error: %v", fErr)
		onStatus(fmt.Sprintf("⚠️ <i>Fetch warning: %v. Proceeding with restart...</i>", fErr))
		return exePath, false, nil
	}
	remoteHash, _ := runCmd(ctx, workDir, "git", "rev-parse", "FETCH_HEAD")
	if remoteHash == "" {
		return exePath, false, nil
	}

	if fresh {
		// fresh container (the image has no .git): is the code baked into the image already
		// this commit? reset --mixed only moves the index, the files stay as they are
		if _, mErr := runCmd(ctx, workDir, "git", "reset", "-q", "--mixed", remoteHash); mErr == nil && gitTreeClean(ctx, workDir) {
			_, _ = runCmd(ctx, workDir, "git", "checkout", "-q", "-B", branch)
			onStatus("• <i>Codebase is already up to date with latest commit.</i>")
			return exePath, false, nil
		}
	} else {
		localHash, _ := runCmd(ctx, workDir, "git", "rev-parse", "HEAD")
		if localHash == remoteHash {
			onStatus("• <i>Codebase is already up to date with latest commit.</i>")
			return exePath, false, nil
		}
		onStatus(fmt.Sprintf("• <i>New commits detected (<code>%.7s</code> ➔ <code>%.7s</code>).</i>", localHash, remoteHash))
	}

	// the new binary goes next to the running one first, then rename over it: writing
	// straight into a running executable fails with "text file busy"
	tmpPath := exePath + ".new"
	defer os.Remove(tmpPath)

	gotBin := false
	if owner, name, ok := githubRepo(repo); ok {
		onStatus(fmt.Sprintf("• <i>Downloading the binary of <code>%.7s</code> from GitHub...</i>", remoteHash))
		dErr := downloadReleaseBinary(ctx, owner, name, branch, remoteHash, tmpPath, binWait, onStatus)
		if dErr == nil {
			gotBin = true
		} else {
			log.Printf("[Updater] release binary: %v", dErr)
			onStatus(fmt.Sprintf("⚠️ <i>No ready binary: %v</i>", dErr))
		}
	}

	if !gotBin {
		// compiling needs about 1GB: never on Heroku (DYNO is set there), only on a VPS / PC
		if os.Getenv("DYNO") != "" {
			onStatus("❌ <i>The binary of the new commit is not ready on GitHub yet (GitHub Actions builds it in a few minutes). Try /restart again later. The bot keeps running the current code.</i>")
			return exePath, false, fmt.Errorf("no binary for %.7s", remoteHash)
		}
		if _, lErr := exec.LookPath("go"); lErr != nil {
			onStatus("⚠️ <i>Go compiler not found in PATH. Restarting existing binary...</i>")
			return exePath, false, nil
		}
		if _, rErr := runCmd(ctx, workDir, "git", "reset", "-q", "--hard", remoteHash); rErr != nil {
			onStatus(fmt.Sprintf("⚠️ <i>Git reset failed: %v</i>", rErr))
			return exePath, false, rErr
		}
		onStatus("• <i>Compiling latest Go binary (<code>go build -ldflags=\"-w -s\"</code>)...</i>")
		buildCmd := exec.CommandContext(ctx, "go", "build", "-ldflags=-w -s", "-o", tmpPath, ".")
		buildCmd.Dir = workDir
		var buildOut bytes.Buffer
		buildCmd.Stdout = &buildOut
		buildCmd.Stderr = &buildOut
		if bErr := buildCmd.Run(); bErr != nil {
			log.Printf("[Updater] Build failed: %v\nOutput: %s", bErr, buildOut.String())
			onStatus(fmt.Sprintf("❌ <b>Compilation Error:</b>\n<pre>%s</pre>\n<i>Restart aborted to keep bot online.</i>", buildOut.String()))
			return exePath, false, fmt.Errorf("build failed: %v", bErr)
		}
	}

	// the code on disk follows the binary
	if _, rErr := runCmd(ctx, workDir, "git", "reset", "-q", "--hard", remoteHash); rErr != nil {
		onStatus(fmt.Sprintf("⚠️ <i>Git reset failed: %v</i>", rErr))
		return exePath, false, rErr
	}
	_, _ = runCmd(ctx, workDir, "git", "checkout", "-q", "-B", branch)

	if rnErr := os.Rename(tmpPath, exePath); rnErr != nil {
		onStatus(fmt.Sprintf("❌ <i>Cannot replace the binary: %v</i>", rnErr))
		return exePath, false, rnErr
	}
	onStatus("✅ <b>Update ready!</b> <i>Restarting process with updated code...</i>")
	return exePath, true, nil
}

// githubRepo: owner and name of a github.com repository URL (also with user:token@).
func githubRepo(repo string) (owner, name string, ok bool) {
	m := ghRepoRe.FindStringSubmatch(strings.TrimSpace(repo))
	if m == nil {
		return "", "", false
	}
	return m[1][strings.LastIndex(m[1], "@")+1:], m[2], true
}

// downloadReleaseBinary: the binary GitHub Actions built for this exact commit. commit.txt of the
// release says which commit it is; while the build of a new push still runs, it waits for it
// (up to wait).
func downloadReleaseBinary(ctx context.Context, owner, name, branch, commit, dst string, wait time.Duration, onStatus func(string)) error {
	base := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s%s/", owner, name, binTagPrefix, branch)
	c := &http.Client{Timeout: 5 * time.Minute}

	deadline := time.Now().Add(wait)
	waited := false
	for {
		got, err := httpGetSmall(ctx, c, base+"commit.txt")
		if err == nil && strings.TrimSpace(got) == commit {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("the GitHub build is still at %.7s", strings.TrimSpace(got))
		}
		if !waited {
			waited = true
			onStatus("• <i>Waiting for GitHub Actions to finish building the new commit...</i>")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Second):
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"azml-go-linux-amd64.gz", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binary download: HTTP %d", resp.StatusCode)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer zr.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, cErr := io.Copy(f, zr)
	if clErr := f.Close(); cErr == nil {
		cErr = clErr
	}
	if cErr != nil {
		return cErr
	}
	// a cut download must never replace the bot: an ELF file of a sane size only
	head := make([]byte, 4)
	if rf, oErr := os.Open(dst); oErr == nil {
		_, _ = io.ReadFull(rf, head)
		rf.Close()
	}
	if n < 5<<20 || string(head) != "\x7fELF" {
		return fmt.Errorf("the downloaded binary is not valid (%d bytes)", n)
	}
	return nil
}

func httpGetSmall(ctx context.Context, c *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return string(b), err
}

// gitTreeClean: the tracked files on disk are the same as the index (HEAD after reset --mixed).
func gitTreeClean(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "diff", "--quiet")
	cmd.Dir = dir
	return cmd.Run() == nil
}

// UpdateAndRestart: /restart. Pulls and compiles the latest code (pullAndBuild), writes
// .restartmsg and replaces the running process with the binary, without a Docker rebuild.
func UpdateAndRestart(ctx context.Context, onStatus func(status string), recordRestartMsg func() error) error {
	exePath, _, err := pullAndBuild(ctx, 6*time.Minute, onStatus)
	if err != nil {
		return err
	}

	// Record restart message
	if recordRestartMsg != nil {
		_ = recordRestartMsg()
	}

	time.Sleep(800 * time.Millisecond)

	log.Printf("[Updater] Initiating process restart for %s...", exePath)
	if rErr := RestartProcess(exePath, os.Args, os.Environ()); rErr != nil {
		log.Printf("[Updater] RestartProcess error: %v, falling back to os.Exit(0)", rErr)
		os.Exit(0)
	}

	return nil
}

// BootUpdate: the update.py step of the Python bot (start.sh: update.py && bot). Runs on every
// start, before the bot takes tasks, so a new container (VPS restart, Heroku dyno cycle,
// docker compose up) runs the latest code of UPSTREAM_REPO / UPSTREAM_BRANCH and not only the
// code baked into the image. When the code changed it replaces the process with the new binary;
// the new process finds HEAD up to date and goes on. NO_BOOT_UPDATE=true turns it off.
func BootUpdate() {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("NO_BOOT_UPDATE"))); v == "true" || v == "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// no waiting at start: when the GitHub build of a new push is not ready the bot starts
	// with the current code, the next start or /restart takes it
	exePath, rebuilt, err := pullAndBuild(ctx, 0, func(s string) { log.Printf("[BootUpdate] %s", s) })
	if err != nil {
		log.Printf("[BootUpdate] %v: starting with the current code", err)
		return
	}
	if !rebuilt {
		return
	}
	log.Printf("[BootUpdate] Starting the updated binary %s", exePath)
	if rErr := RestartProcess(exePath, os.Args, os.Environ()); rErr != nil {
		log.Printf("[BootUpdate] RestartProcess error: %v: starting with the current code", rErr)
	}
}
