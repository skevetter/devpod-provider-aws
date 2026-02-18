package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	providerName = "aws"
	githubOwner  = "skevetter"
	githubRepo   = "devpod-provider-aws"
)

type Provider struct {
	Name         string            `yaml:"name"`
	Version      string            `yaml:"version"`
	Description  string            `yaml:"description"`
	Icon         string            `yaml:"icon"`
	IconDark     string            `yaml:"iconDark"`
	OptionGroups []OptionGroup     `yaml:"optionGroups"`
	Options      Options           `yaml:"options"`
	Agent        Agent             `yaml:"agent"`
	Binaries     Binaries          `yaml:"binaries"`
	Exec         map[string]string `yaml:"exec"`
}

type OptionGroup struct {
	Name           string   `yaml:"name"`
	DefaultVisible bool     `yaml:"defaultVisible"`
	Options        []string `yaml:"options"`
}

type Options map[string]Option

type Option struct {
	Description string   `yaml:"description,omitempty"`
	Required    bool     `yaml:"required,omitempty"`
	Default     string   `yaml:"default,omitempty"`
	Type        string   `yaml:"type,omitempty"`
	Suggestions []string `yaml:"suggestions,omitempty"`
	Command     string   `yaml:"command,omitempty"`
	Password    bool     `yaml:"password,omitempty"`
}

type Agent struct {
	Path                    string         `yaml:"path"`
	InactivityTimeout       string         `yaml:"inactivityTimeout"`
	InjectGitCredentials    string         `yaml:"injectGitCredentials"`
	InjectDockerCredentials string         `yaml:"injectDockerCredentials"`
	Binaries                map[string]any `yaml:"binaries"`
	Exec                    map[string]any `yaml:"exec"`
}

type Binaries struct {
	AWSProvider []Binary `yaml:"AWS_PROVIDER"`
}

type Binary struct {
	OS       string `yaml:"os"`
	Arch     string `yaml:"arch"`
	Path     string `yaml:"path"`
	Checksum string `yaml:"checksum"`
}

type buildConfig struct {
	version     string
	projectRoot string
	isRelease   bool
	checksums   map[string]string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("expected version as argument")
	}

	cfg, err := newBuildConfig(os.Args[1])
	if err != nil {
		return err
	}

	provider := buildProvider(cfg)

	output, err := yaml.Marshal(provider)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}

	fmt.Print(string(output))
	return nil
}

func newBuildConfig(version string) (*buildConfig, error) {
	checksums, err := parseChecksums("./dist/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("parse checksums: %w", err)
	}

	projectRoot := os.Getenv("PROJECT_ROOT")
	if projectRoot == "" {
		owner := getEnvOrDefault("GITHUB_OWNER", githubOwner)
		projectRoot = fmt.Sprintf("https://github.com/%s/%s/releases/download/%s", owner, githubRepo, version)
	}

	// Only treat as release if it's a GitHub release URL
	isRelease := strings.Contains(projectRoot, "github.com") && strings.Contains(projectRoot, "/releases/")

	return &buildConfig{
		version:     version,
		projectRoot: projectRoot,
		isRelease:   isRelease,
		checksums:   checksums,
	}, nil
}

func buildProvider(cfg *buildConfig) Provider {
	return Provider{
		Name:         providerName,
		Version:      cfg.version,
		Description:  "DevPod on AWS Cloud",
		Icon:         "https://devpod.sh/assets/aws.svg",
		IconDark:     "https://devpod.sh/assets/aws_dark.svg",
		OptionGroups: buildOptionGroups(),
		Options:      buildOptions(),
		Agent:        buildAgent(cfg),
		Binaries:     buildBinaries(cfg, allPlatforms()),
		Exec: map[string]string{
			"init":    "${AWS_PROVIDER} init",
			"command": "${AWS_PROVIDER} command",
			"create":  "${AWS_PROVIDER} create",
			"delete":  "${AWS_PROVIDER} delete",
			"start":   "${AWS_PROVIDER} start",
			"stop":    "${AWS_PROVIDER} stop",
			"status":  "${AWS_PROVIDER} status",
		},
	}
}

func buildOptionGroups() []OptionGroup {
	return []OptionGroup{
		{
			Name:           "AWS options",
			DefaultVisible: false,
			Options: []string{
				"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE",
				"AWS_AMI", "AWS_DISK_SIZE", "AWS_ROOT_DEVICE", "AWS_INSTANCE_TYPE",
				"AWS_VPC_ID", "AWS_SUBNET_ID", "AWS_SECURITY_GROUP_ID", "AWS_INSTANCE_PROFILE_ARN",
				"AWS_INSTANCE_TAGS", "AWS_USE_INSTANCE_CONNECT_ENDPOINT", "AWS_INSTANCE_CONNECT_ENDPOINT_ID",
				"AWS_USE_SPOT_INSTANCE", "AWS_USE_SESSION_MANAGER", "AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER",
				"AWS_USE_ROUTE53", "AWS_ROUTE53_ZONE_NAME", "AWS_AVAILABILITY_ZONE", "AWS_USE_NESTED_VIRTUALIZATION",
			},
		},
		{
			Name:           "Agent options",
			DefaultVisible: false,
			Options:        []string{"AGENT_PATH", "INACTIVITY_TIMEOUT", "INJECT_DOCKER_CREDENTIALS", "INJECT_GIT_CREDENTIALS"},
		},
		{
			Name:           "Credential handling options",
			DefaultVisible: true,
			Options:        []string{"CUSTOM_AWS_CREDENTIAL_COMMAND"},
		},
	}
}

func buildOptions() Options {
	return Options{
		"AWS_REGION": {
			Description: "The aws cloud region to create the VM in. E.g. us-west-1",
			Required:    true,
			Command:     `printf "%s" "${AWS_DEFAULT_REGION:-$(aws configure get region)}" || true`,
			Suggestions: []string{
				"ap-south-1", "eu-north-1", "eu-west-3", "eu-west-2", "eu-west-1",
				"ap-northeast-3", "ap-northeast-2", "ap-northeast-1", "ca-central-1",
				"sa-east-1", "ap-southeast-1", "ap-southeast-2", "eu-central-1",
				"us-east-1", "us-east-2", "us-west-1", "us-west-2",
			},
		},
		"AWS_ACCESS_KEY_ID": {
			Description: "The aws access key id",
			Command:     `printf "%s" "${AWS_ACCESS_KEY_ID:-}"`,
		},
		"AWS_SECRET_ACCESS_KEY": {
			Description: "The aws secret access key",
			Password:    true,
			Command:     `printf "%s" "${AWS_SECRET_ACCESS_KEY:-}"`,
		},
		"AWS_SESSION_TOKEN": {
			Description: "The aws session token for temporary credentials",
			Password:    true,
			Command:     `printf "%s" "${AWS_SESSION_TOKEN:-}"`,
		},
		"AWS_PROFILE": {
			Description: "The aws profile name to use",
			Command:     `printf "%s" "${AWS_PROFILE:-default}"`,
		},
		"AWS_DISK_SIZE": {
			Description: "The disk size to use.",
			Default:     "40",
		},
		"AWS_ROOT_DEVICE": {
			Description: "The root device of the disk image.",
			Default:     "",
		},
		"AWS_VPC_ID": {
			Description: "The vpc id to use.",
			Default:     "",
		},
		"AWS_SUBNET_ID": {
			Description: "The subnet id to use. Can also be multiple once separated by a comma. By default the one with the most available IPs is chosen. Can be overridden by AWS_AVAILABILITY_ZONE.",
			Default:     "",
		},
		"AWS_SECURITY_GROUP_ID": {
			Description: "The security group id to use. Multiple can be specified by separating with a comma.",
			Default:     "",
		},
		"AWS_AVAILABILITY_ZONE": {
			Description: "The name of the AWS availability zone can be specified to choose a subnet out of the desired zone.",
			Default:     "",
		},
		"AWS_AMI": {
			Description: "The disk image to use.",
			Default:     "",
		},
		"AWS_INSTANCE_PROFILE_ARN": {
			Description: "The instance profile ARN to use",
			Default:     "",
		},
		"AWS_INSTANCE_TAGS": {
			Description: "Additional flags to add to the instance in the form of \"Name=XXX,Value=YYY Name=ZZZ,Value=WWW\"",
			Default:     "",
		},
		"AWS_INSTANCE_TYPE": {
			Description: "The machine type to use.",
			Default:     "c5.xlarge",
			Suggestions: []string{
				"t2.2xlarge", "t2.large", "t2.medium", "t2.nano", "t2.small", "t2.xlarge",
				"t3.2xlarge", "t3.large", "t3.medium", "t3.nano", "t3.small", "t3.xlarge",
				"t3a.2xlarge", "t3a.large", "t3a.medium", "t3a.nano", "t3a.small", "t3a.xlarge",
				"t4g.2xlarge", "t4g.large", "t4g.medium", "t4g.nano", "t4g.small", "t4g.xlarge",
				"c4.2xlarge", "c4.4xlarge", "c4.8xlarge", "c4.large", "c4.xlarge",
				"c5.12xlarge", "c5.18xlarge", "c5.24xlarge", "c5.2xlarge", "c5.4xlarge", "c5.9xlarge", "c5.large", "c5.xlarge",
				"c5a.12xlarge", "c5a.16xlarge", "c5a.24xlarge", "c5a.2xlarge", "c5a.4xlarge", "c5a.8xlarge", "c5a.large", "c5a.xlarge",
				"c6a.12xlarge", "c6a.16xlarge", "c6a.24xlarge", "c6a.2xlarge", "c6a.32xlarge", "c6a.48xlarge", "c6a.4xlarge", "c6a.8xlarge", "c6a.large", "c6a.xlarge",
				"c6g.12xlarge", "c6g.16xlarge", "c6g.2xlarge", "c6g.4xlarge", "c6g.8xlarge", "c6g.large", "c6g.medium", "c6g.xlarge",
				"c6i.12xlarge", "c6i.16xlarge", "c6i.24xlarge", "c6i.2xlarge", "c6i.32xlarge", "c6i.4xlarge", "c6i.8xlarge", "c6i.large", "c6i.xlarge",
				"c7g.12xlarge", "c7g.16xlarge", "c7g.2xlarge", "c7g.4xlarge", "c7g.8xlarge", "c7g.large", "c7g.medium", "c7g.xlarge",
				"cc2.8xlarge",
				"m4.10xlarge", "m4.16xlarge", "m4.2xlarge", "m4.4xlarge", "m4.large", "m4.xlarge",
				"m5.12xlarge", "m5.16xlarge", "m5.24xlarge", "m5.2xlarge", "m5.4xlarge", "m5.8xlarge", "m5.large", "m5.xlarge",
				"m5a.12xlarge", "m5a.16xlarge", "m5a.24xlarge", "m5a.2xlarge", "m5a.4xlarge", "m5a.8xlarge", "m5a.large", "m5a.xlarge",
				"m6a.12xlarge", "m6a.16xlarge", "m6a.24xlarge", "m6a.2xlarge", "m6a.32xlarge", "m6a.48xlarge", "m6a.4xlarge", "m6a.8xlarge", "m6a.large", "m6a.xlarge",
				"m6g.12xlarge", "m6g.16xlarge", "m6g.2xlarge", "m6g.4xlarge", "m6g.8xlarge", "m6g.large", "m6g.medium", "m6g.xlarge",
				"m6i.12xlarge", "m6i.16xlarge", "m6i.24xlarge", "m6i.2xlarge", "m6i.32xlarge", "m6i.4xlarge", "m6i.8xlarge", "m6i.large", "m6i.xlarge",
				"m7g.12xlarge", "m7g.16xlarge", "m7g.2xlarge", "m7g.4xlarge", "m7g.8xlarge", "m7g.large", "m7g.medium", "m7g.xlarge",
			},
		},
		"AWS_USE_NESTED_VIRTUALIZATION": {
			Description: "If defined, nested virtualization will be enabled for the EC2 instance.",
			Type:        "boolean",
			Default:     "false",
		},
		"AWS_USE_INSTANCE_CONNECT_ENDPOINT": {
			Description: "If defined, will try to connect to the ec2 instance via the default instance connect endpoint for the current subnet",
			Type:        "boolean",
			Default:     "false",
		},
		"AWS_INSTANCE_CONNECT_ENDPOINT_ID": {
			Description: "Specify which instance connect endpoint to use. Only works with AWS_USE_INSTANCE_CONNECT_ENDPOINT enabled",
			Default:     "",
		},
		"AWS_USE_SPOT_INSTANCE": {
			Description: "Prefer the Spot instead of On-Demand instances.",
			Type:        "boolean",
			Default:     "false",
		},
		"AWS_USE_SESSION_MANAGER": {
			Description: "If defined, will try to connect to the ec2 instance via the AWS Session Manager",
			Type:        "boolean",
			Default:     "false",
		},
		"AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER": {
			Description: "Specify the KMS key ARN to use for the AWS Session Manager",
			Default:     "",
		},
		"AWS_USE_ROUTE53": {
			Description: "If defined, will try to create a Route53 record for the machine's IP address and use that hostname upon machine connection. If activated, the Route53 zone can be configured by AWS_ROUTE53_ZONE_NAME or of not, it is tried to lookup by the tag `devpod=devpod`",
			Type:        "boolean",
			Default:     "false",
		},
		"AWS_ROUTE53_ZONE_NAME": {
			Description: "The zone name of a Route53 hosted zone to use for the machine's DNS name",
			Default:     "",
		},
		"INACTIVITY_TIMEOUT": {
			Description: "If defined, will automatically stop the VM after the inactivity period.",
			Default:     "10m",
		},
		"INJECT_GIT_CREDENTIALS": {
			Description: "If DevPod should inject git credentials into the remote host.",
			Default:     "true",
		},
		"INJECT_DOCKER_CREDENTIALS": {
			Description: "If DevPod should inject docker credentials into the remote host.",
			Default:     "true",
		},
		"AGENT_PATH": {
			Description: "The path where to inject the DevPod agent to.",
			Default:     "/var/lib/toolbox/devpod",
		},
		"CUSTOM_AWS_CREDENTIAL_COMMAND": {
			Description: "Shell command which is executed to get the AWS credentials. The command must return a json containing the keys `AccessKeyID` (required), `SecretAccessKey` (required) and `SessionToken` (optional).",
			Default:     "",
		},
	}
}

func buildAgent(cfg *buildConfig) Agent {
	return Agent{
		Path:                    "${AGENT_PATH}",
		InactivityTimeout:       "${INACTIVITY_TIMEOUT}",
		InjectGitCredentials:    "${INJECT_GIT_CREDENTIALS}",
		InjectDockerCredentials: "${INJECT_DOCKER_CREDENTIALS}",
		Binaries: map[string]any{
			"AWS_PROVIDER": buildBinaries(cfg, linuxPlatforms()).AWSProvider,
		},
		Exec: map[string]any{
			"shutdown": "${AWS_PROVIDER} stop || shutdown",
		},
	}
}

func buildBinaries(cfg *buildConfig, platforms []string) Binaries {
	return Binaries{AWSProvider: buildBinaryList(cfg, platforms)}
}

func buildBinaryList(cfg *buildConfig, platforms []string) []Binary {
	result := make([]Binary, 0, len(platforms))
	for _, platform := range platforms {
		result = append(result, buildBinary(cfg, platform))
	}
	return result
}

func buildBinary(cfg *buildConfig, platform string) Binary {
	os, arch, _ := strings.Cut(platform, "/")

	path := cfg.projectRoot
	if !cfg.isRelease {
		if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
			base, _ := url.Parse(path)
			joined, _ := url.JoinPath(base.String(), buildDir(platform))
			path = joined
		} else {
			absPath, _ := filepath.Abs(path)
			path = filepath.Join(absPath, buildDir(platform))
		}
	}

	filename := fmt.Sprintf("devpod-provider-%s-%s-%s", providerName, os, arch)
	if os == "windows" {
		filename += ".exe"
	}

	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		path, _ = url.JoinPath(path, filename)
	} else {
		path = filepath.Join(path, filename)
	}

	return Binary{
		OS:       os,
		Arch:     arch,
		Path:     path,
		Checksum: cfg.checksums[filename],
	}
}

func buildDir(platform string) string {
	dirs := map[string]string{
		"linux/amd64":   "build_linux_amd64_v1",
		"linux/arm64":   "build_linux_arm64_v8.0",
		"darwin/amd64":  "build_darwin_amd64_v1",
		"darwin/arm64":  "build_darwin_arm64_v8.0",
		"windows/amd64": "build_windows_amd64_v1",
	}
	return dirs[platform]
}

func allPlatforms() []string {
	return []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"}
}

func linuxPlatforms() []string {
	return []string{"linux/amd64", "linux/arm64"}
}

func parseChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if checksum, filename, ok := strings.Cut(scanner.Text(), " "); ok {
			checksums[strings.TrimSpace(filename)] = checksum
		}
	}

	return checksums, scanner.Err()
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
