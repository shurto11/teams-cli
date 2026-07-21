package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// tokenRefreshScriptName はトークンディレクトリ内で探すリフレッシュスクリプト名。
	tokenRefreshScriptName = "teams-refresh.sh"
	// tokenRefreshScriptEnv でスクリプトのフルパスを明示的に上書きできる。
	tokenRefreshScriptEnv = "TEAMS_REFRESH_SCRIPT"
	// tokenRefreshTimeout は curl が固まったときに起動をブロックし続けないための上限。
	tokenRefreshTimeout = 45 * time.Second
)

// refreshTokensAtStartup はトークンを読み込む前に teams-refresh.sh refresh を実行し、
// 3つの JWT を最新化する。スクリプトが見つからない場合や失敗した場合でも
// 既存トークンでの起動を妨げないよう、致命的エラーにはしない。
func refreshTokensAtStartup(logger *logrus.Logger, tokenDir string) {
	script, ok := locateTokenRefreshScript(tokenDir)
	if !ok {
		logger.WithField("token_dir", effectiveTokenDir(tokenDir)).
			Debug("token refresh script not found; skipping startup refresh")
		return
	}

	logger.WithField("script", script).Info("refreshing tokens before startup")

	ctx, cancel := context.WithTimeout(context.Background(), tokenRefreshTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", script, "refresh")
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))

	if err != nil {
		fields := logrus.Fields{"script": script}
		if ctx.Err() == context.DeadlineExceeded {
			fields["timeout"] = tokenRefreshTimeout.String()
		}
		if trimmed != "" {
			fields["output"] = trimmed
		}
		logger.WithFields(fields).WithError(err).
			Warn("startup token refresh failed; continuing with existing tokens")
		return
	}

	if trimmed != "" {
		logger.WithField("output", trimmed).Debug("startup token refresh completed")
	} else {
		logger.Debug("startup token refresh completed")
	}
}

// locateTokenRefreshScript は実行するスクリプトのパスを解決する。
// TEAMS_REFRESH_SCRIPT 環境変数があればそれを優先し、
// なければトークンディレクトリ内の teams-refresh.sh を使う。
func locateTokenRefreshScript(tokenDir string) (string, bool) {
	if override := strings.TrimSpace(os.Getenv(tokenRefreshScriptEnv)); override != "" {
		if fileExists(override) {
			return override, true
		}
		return "", false
	}

	script := filepath.Join(effectiveTokenDir(tokenDir), tokenRefreshScriptName)
	if fileExists(script) {
		return script, true
	}

	return "", false
}

// effectiveTokenDir は --token-dir 未指定時に既定の ~/.config/fossteams を返す。
func effectiveTokenDir(tokenDir string) string {
	if strings.TrimSpace(tokenDir) != "" {
		return tokenDir
	}
	if dir, err := defaultTokenDir(); err == nil {
		return dir
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
