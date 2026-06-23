package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func upgrade(args cliArgs) error {
	if _, err := exec.LookPath("curl"); err != nil {
		return errors.New("missing required command: curl")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return errors.New("missing required command: sh")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := os.Environ()
	env = append(env, "ABRA_INSTALL_DIR="+filepath.Dir(exe))
	target := flag(args, "version", "")
	script := strings.TrimSpace(os.Getenv("ABRA_INSTALL_SCRIPT"))
	if script == "" {
		script = installScript
		if target != "" {
			script = releaseInstallScriptURL(target)
		}
	}
	if target != "" {
		env = append(env, "ABRA_VERSION="+target)
	}
	tmpDir, err := os.MkdirTemp("", "abra-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	scriptPath := filepath.Join(tmpDir, "install.sh")
	download := exec.Command("curl", "-fsSL", script, "-o", scriptPath)
	download.Env = env
	if output, err := download.CombinedOutput(); err != nil {
		return installScriptDownloadError(script, err, output)
	}
	if err := verifyInstallScriptAttestation(scriptPath); err != nil {
		return err
	}
	cmd := exec.Command("sh", scriptPath)
	cmd.Env = append(env, "ABRA_INSTALL_SCRIPT="+script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func releaseInstallScriptURL(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		return installScript
	}
	return "https://github.com/hermawan22/abra/releases/download/" + url.PathEscape(version) + "/install.sh"
}

func installScriptDownloadError(script string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		detail = "\n" + detail
	}
	return fmt.Errorf(`download Abra install script failed: %w
script: %s%s

Recovery:
  1. Check the installer URL. The official script is:
     %s
  2. If you are using a fork, publish a signed release and set ABRA_INSTALL_SCRIPT to that release's install.sh URL.
  3. If you want a specific release, run: abra upgrade --version vX.Y.Z`, err, script, detail, installScript)
}

func uninstall(args cliArgs) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !boolFlag(args, "yes") {
		fmt.Println("This removes the Abra CLI binary only. It does not remove Docker containers, volumes, env files, or memory data.")
		fmt.Println("Binary: " + exe)
		fmt.Println("Run: abra uninstall --yes")
		return nil
	}
	if err := os.Remove(exe); err != nil {
		return err
	}
	fmt.Println("Removed: " + exe)
	fmt.Println("Local stack data was left untouched. Run `abra down --reset` before uninstalling when you also want demo data removed.")
	return nil
}

func demo(ctx context.Context, args cliArgs) error {
	args.Bools["demo"] = true
	if err := up(ctx, args); err != nil {
		return err
	}
	scope := flag(args, "scope", "repo:abra-demo-"+timestamp())
	if err := ingest(ctx, args, map[string]any{
		"source_type": "markdown",
		"source_url":  "file://abra-demo-" + timestamp() + ".md",
		"title":       "Abra Demo",
		"scope":       scope,
		"content": strings.Join([]string{
			"Abra is an agent-first governed brain layer for AI agents.",
			"Agents should use Abra before autonomous code changes.",
			"Abra returns citations, graph context, gap analysis, memory health, and an agent decision gate.",
		}, "\n"),
		"authority": "official-doc",
	}); err != nil {
		return err
	}
	result, err := callMCPTool(ctx, args, "brain_think", map[string]any{
		"question":    "What should agents use before autonomous code changes?",
		"scope":       scope,
		"limit":       5,
		"max_queries": 4,
	})
	if err != nil {
		return err
	}
	printThink(result, "full")
	printReady(args)
	return nil
}

func initEnv(args cliArgs) error {
	path := envPath(args)
	if fileExists(path) && !boolFlag(args, "force") {
		fmt.Printf("Env already exists: %s\n", path)
		fmt.Println("Use --force to overwrite.")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if shouldHydrateRuntimeBeforeEnv(args) {
		if _, err := ensureProjectDir(context.Background(), args); err != nil {
			return err
		}
	}
	content := demoEnv()
	if boolFlag(args, "production") {
		content = productionEnvExample
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", path)
	if boolFlag(args, "production") {
		fmt.Println("Edit placeholders before running: abra up --env-file " + path)
	}
	return nil
}

func shouldHydrateRuntimeBeforeEnv(args cliArgs) bool {
	if boolFlag(args, "production") || isAbraSourceCheckout(".") || runtimeVersion() == "main" {
		return false
	}
	return strings.TrimSpace(os.Getenv("ABRA_RELEASE_BASE_URL")) != "" || strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")) != ""
}
