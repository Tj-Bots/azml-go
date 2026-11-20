package ext

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// pullAndBuild: like update.py of the Python bot. Fetches UPSTREAM_REPO / UPSTREAM_BRANCH,
// resets the code to it and compiles a new binary when the code changed.
// rebuilt is true only when a new binary was written to exePath.
func pullAndBuild(ctx context.Context, onStatus func(status string)) (exePath string, rebuilt bool, err error) {
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

	workDir, _ := os.Getwd()
	updated := false

	if repo != "" && hasGit {
		gitDir := filepath.Join(workDir, ".git")
		if _, statErr := os.Stat(gitDir); os.IsNotExist(statErr) {
			// fresh container (the image has no .git): reset --hard also overwrites the copied files,
			// a checkout would refuse because they are untracked
			onStatus(fmt.Sprintf("• <i>Initializing Git and cloning <code>%s</code> (%s)...</i>", repo, branch))
			_, _ = runCmd(ctx, workDir, "git", "init", "-q")
			_, _ = runCmd(ctx, workDir, "git", "config", "user.email", "bot@azml.local")
			_, _ = runCmd(ctx, workDir, "git", "config", "user.name", "AZML-Go")
			// the Dockerfile runs chmod -R 777: modes are not a change
			_, _ = runCmd(ctx, workDir, "git", "config", "core.fileMode", "false")
			_, _ = runCmd(ctx, workDir, "git", "remote", "add", "origin", repo)
			if _, fErr := runCmd(ctx, workDir, "git", "fetch", "-q", "origin", branch); fErr != nil {
				log.Printf("[Updater] git fetch error: %v", fErr)
				onStatus(fmt.Sprintf("⚠️ <i>Fetch warning: %v</i>", fErr))
			} else if _, mErr := runCmd(ctx, workDir, "git", "reset", "-q", "--mixed", "FETCH_HEAD"); mErr == nil &&
				gitTreeClean(ctx, workDir) {
				// the code baked into the image is already the latest commit: nothing to compile
				_, _ = runCmd(ctx, workDir, "git", "checkout", "-q", "-B", branch)
				onStatus("• <i>Codebase is already up to date with latest commit.</i>")
			} else if _, rErr := runCmd(ctx, workDir, "git", "reset", "-q", "--hard", "FETCH_HEAD"); rErr == nil {
				_, _ = runCmd(ctx, workDir, "git", "checkout", "-q", "-B", branch)
				updated = true
			} else {
				onStatus(fmt.Sprintf("⚠️ <i>Git reset failed: %v</i>", rErr))
			}
		} else {
			onStatus(fmt.Sprintf("• <i>Checking for updates from <code>%s</code> (%s)...</i>", repo, branch))
			_, _ = runCmd(ctx, workDir, "git", "remote", "set-url", "origin", repo)
			if _, fErr := runCmd(ctx, workDir, "git", "fetch", "-q", "origin", branch); fErr != nil {
				log.Printf("[Updater] git fetch error: %v", fErr)
				onStatus(fmt.Sprintf("⚠️ <i>Fetch warning: %v. Proceeding with restart...</i>", fErr))
			} else {
				localHash, _ := runCmd(ctx, workDir, "git", "rev-parse", "HEAD")
				remoteHash, _ := runCmd(ctx, workDir, "git", "rev-parse", "FETCH_HEAD")

				if remoteHash != "" && localHash != remoteHash {
					onStatus(fmt.Sprintf("• <i>New commits detected (<code>%.7s</code> ➔ <code>%.7s</code>). Pulling latest code...</i>", localHash, remoteHash))
					if _, rErr := runCmd(ctx, workDir, "git", "reset", "-q", "--hard", "FETCH_HEAD"); rErr == nil {
						updated = true
					} else {
						onStatus(fmt.Sprintf("⚠️ <i>Git reset failed: %v</i>", rErr))
					}
				} else {
					onStatus("• <i>Codebase is already up to date with latest commit.</i>")
				}
			}
		}
	} else if repo == "" {
		onStatus("• <i>No UPSTREAM_REPO configured. Performing direct binary restart...</i>")
	} else {
		onStatus("⚠️ <i>Git not found in PATH. Skipping code pull and restarting current binary...</i>")
	}

	if !updated {
		return exePath, false, nil
	}

	goPath, lErr := exec.LookPath("go")
	if lErr != nil || goPath == "" {
		onStatus("⚠️ <i>Go compiler not found in PATH. Code pulled but cannot compile. Restarting existing binary...</i>")
		return exePath, false, nil
	}

	onStatus("• <i>Compiling latest Go binary (<code>go build -ldflags=\"-w -s\"</code>)...</i>")
	// build next to the running binary, then rename over it: writing straight into a running
	// executable fails with "text file busy"
	tmpPath := exePath + ".new"
	buildCmd := exec.CommandContext(ctx, "go", "build", "-ldflags=-w -s", "-o", tmpPath, ".")
	buildCmd.Dir = workDir
	var buildOut bytes.Buffer
	buildCmd.Stdout = &buildOut
	buildCmd.Stderr = &buildOut

	if bErr := buildCmd.Run(); bErr != nil {
		_ = os.Remove(tmpPath)
		log.Printf("[Updater] Build failed: %v\nOutput: %s", bErr, buildOut.String())
		onStatus(fmt.Sprintf("❌ <b>Compilation Error:</b>\n<pre>%s</pre>\n<i>Restart aborted to keep bot online.</i>", buildOut.String()))
		return exePath, false, fmt.Errorf("build failed: %v", bErr)
	}
	if rnErr := os.Rename(tmpPath, exePath); rnErr != nil {
		_ = os.Remove(tmpPath)
		onStatus(fmt.Sprintf("❌ <i>Cannot replace the binary: %v</i>", rnErr))
		return exePath, false, rnErr
	}
	onStatus("✅ <b>Compilation successful!</b> <i>Restarting process with updated code...</i>")
	return exePath, true, nil
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
	exePath, _, err := pullAndBuild(ctx, onStatus)
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
	exePath, rebuilt, err := pullAndBuild(ctx, func(s string) { log.Printf("[BootUpdate] %s", s) })
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
