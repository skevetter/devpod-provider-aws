package aws

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type HookSuite struct {
	suite.Suite
}

func TestHookSuite(t *testing.T) {
	suite.Run(t, new(HookSuite))
}

func (s *HookSuite) TestIsS3Reference() {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{name: "s3 URI", value: "s3://bucket/script.sh", expected: true},
		{name: "s3 URI with deep path", value: "s3://bucket/a/b/c.sh", expected: true},
		{name: "s3 URI minimal", value: "s3://b", expected: true},
		{name: "inline command", value: "apt-get update", expected: false},
		{name: "empty string", value: "", expected: false},
		{name: "uppercase S3", value: "S3://bucket", expected: false},
		{name: "s3 without colon-slash-slash", value: "s3:bucket", expected: false},
		{name: "https URL", value: "https://example.com/script.sh", expected: false},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			assert.Equal(s.T(), tt.expected, isS3Reference(tt.value))
		})
	}
}

func (s *HookSuite) TestHookSnippetEmpty() {
	result := hookSnippet("post-ssh", "")
	assert.Equal(s.T(), "", result)
}

func (s *HookSuite) TestHookSnippetInline() {
	result := hookSnippet("post-ssh", "apt-get update")

	assert.Contains(s.T(), result, "# --- Hook: post-ssh ---")
	assert.Contains(s.T(), result, "base64 -d > /tmp/devpod-hook-post-ssh.sh")
	assert.Contains(s.T(), result,
		"/bin/sh /tmp/devpod-hook-post-ssh.sh >> /var/log/devpod-hook-post-ssh.log 2>&1")
	assert.Contains(s.T(), result, "rm -f /tmp/devpod-hook-post-ssh.sh")

	// Verify base64 round-trip: the encoded value should decode back to the original
	encoded := base64.StdEncoding.EncodeToString([]byte("apt-get update"))
	assert.Contains(s.T(), result, encoded)
}

func (s *HookSuite) TestHookSnippetInlineWithPercentChars() {
	input := `awk '{print $1 "%" $2}'`
	result := hookSnippet("post-ssh", input)

	// Base64 encoding eliminates the % problem entirely
	encoded := base64.StdEncoding.EncodeToString([]byte(input))
	assert.Contains(s.T(), result, encoded)
}

func (s *HookSuite) TestHookSnippetInlineMultiLine() {
	input := "line1\nline2\nline3"
	result := hookSnippet("post-ssh", input)

	encoded := base64.StdEncoding.EncodeToString([]byte(input))
	assert.Contains(s.T(), result, encoded)
}

func (s *HookSuite) TestHookSnippetS3() {
	result := hookSnippet("post-ssh", "s3://my-bucket/scripts/setup.sh")

	assert.Contains(s.T(), result, "# --- Hook: post-ssh ---")
	assert.Contains(s.T(), result,
		"aws s3 cp s3://my-bucket/scripts/setup.sh /tmp/devpod-hook-post-ssh.sh")
	assert.Contains(s.T(), result,
		"/bin/sh /tmp/devpod-hook-post-ssh.sh >> /var/log/devpod-hook-post-ssh.log 2>&1")
	assert.Contains(s.T(), result, "WARNING: post-ssh hook S3 download failed")
	assert.Contains(s.T(), result, "rm -f /tmp/devpod-hook-post-ssh.sh")
}

func (s *HookSuite) TestHookSnippetS3DeepPath() {
	result := hookSnippet("post-volume", "s3://bucket/a/b/c.sh")

	assert.Contains(s.T(), result,
		"aws s3 cp s3://bucket/a/b/c.sh /tmp/devpod-hook-post-volume.sh")
	assert.Contains(s.T(), result, "/var/log/devpod-hook-post-volume.log")
}

func (s *HookSuite) TestHookSnippetS3ShellEscape() {
	// URI with shell metacharacters must be escaped
	result := hookSnippet("post-ssh", "s3://bucket/$(whoami).sh")

	assert.Contains(s.T(), result,
		"aws s3 cp 's3://bucket/$(whoami).sh'")
	assert.NotContains(s.T(), result, `"s3://bucket/$(whoami).sh"`)
}

func (s *HookSuite) TestHookSnippetHookNameInOutput() {
	result := hookSnippet("post-volume", "echo hello")

	assert.Contains(s.T(), result, "# --- Hook: post-volume ---")
	assert.Contains(s.T(), result, "/tmp/devpod-hook-post-volume.sh")
	assert.Contains(s.T(), result, "/var/log/devpod-hook-post-volume.log")
}
