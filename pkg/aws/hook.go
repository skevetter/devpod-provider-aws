package aws

import (
	"encoding/base64"
	"fmt"
	"strings"

	"al.essio.dev/pkg/shellescape"
)

const (
	hookTmpPrefix = "/tmp/devpod-hook-"
	hookLogPrefix = "/var/log/devpod-hook-"
)

// isS3Reference returns true if the value is an S3 URI (case-sensitive s3:// prefix).
func isS3Reference(value string) bool {
	return strings.HasPrefix(value, "s3://")
}

// hookSnippet returns a shell snippet that executes a user-data hook.
// If value is empty, returns "". If value starts with s3://, generates a
// snippet that fetches the script from S3. Otherwise, base64-encodes the
// value and generates a snippet that decodes and executes it.
// All output is logged to /var/log/devpod-hook-<name>.log.
// Failures emit a warning to stderr but do not halt the script.
func hookSnippet(name string, value string) string {
	if value == "" {
		return ""
	}

	tmpFile := hookTmpPrefix + name + ".sh"
	logFile := hookLogPrefix + name + ".log"

	if isS3Reference(value) {
		return s3HookSnippet(name, value, tmpFile, logFile)
	}

	return inlineHookSnippet(name, value, tmpFile, logFile)
}

func inlineHookSnippet(name, commands, tmpFile, logFile string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(commands))

	return fmt.Sprintf(`

# --- Hook: %s ---
echo "%s" | base64 -d > %s
chmod +x %s
/bin/sh %s >> %s 2>&1 || echo "WARNING: %s hook failed (see %s)" >&2
rm -f %s`,
		name,
		encoded,
		tmpFile,
		tmpFile,
		tmpFile, logFile,
		name, logFile,
		tmpFile,
	)
}

func s3HookSnippet(name, s3URI, tmpFile, logFile string) string {
	safeURI := shellescape.Quote(s3URI)

	return fmt.Sprintf(`

# --- Hook: %s ---
if aws s3 cp %s %s >> %s 2>&1; then
  chmod +x %s
  /bin/sh %s >> %s 2>&1 || echo "WARNING: %s hook failed (see %s)" >&2
else
  echo "WARNING: %s hook S3 download failed (see %s)" >&2
fi
rm -f %s`,
		name,
		safeURI, tmpFile, logFile,
		tmpFile,
		tmpFile, logFile,
		name, logFile,
		name, logFile,
		tmpFile,
	)
}
