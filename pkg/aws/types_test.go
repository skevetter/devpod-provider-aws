package aws

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestNewMachineFromInstance(t *testing.T) {
	tests := []struct {
		name     string
		instance types.Instance
		expected Machine
	}{
		{
			name: "instance with all fields",
			instance: types.Instance{
				InstanceId:            aws.String("i-1234567890abcdef0"),
				PublicIpAddress:       aws.String("203.0.113.1"),
				PrivateIpAddress:      aws.String("10.0.1.5"),
				SpotInstanceRequestId: aws.String("sir-abc123"),
				State: &types.InstanceState{
					Name: types.InstanceStateNameRunning,
				},
				Tags: []types.Tag{
					{
						Key:   aws.String(tagKeyHostname),
						Value: aws.String("test.example.com"),
					},
				},
			},
			expected: Machine{
				InstanceID:            "i-1234567890abcdef0",
				PublicIP:              "203.0.113.1",
				PrivateIP:             "10.0.1.5",
				SpotInstanceRequestId: "sir-abc123",
				Status:                "running",
				Hostname:              "test.example.com",
			},
		},
		{
			name: "instance without public IP",
			instance: types.Instance{
				InstanceId:       aws.String("i-0987654321fedcba0"),
				PrivateIpAddress: aws.String("10.0.2.10"),
				State: &types.InstanceState{
					Name: types.InstanceStateNameStopped,
				},
			},
			expected: Machine{
				InstanceID:            "i-0987654321fedcba0",
				PublicIP:              "",
				PrivateIP:             "10.0.2.10",
				SpotInstanceRequestId: "",
				Status:                "stopped",
				Hostname:              "",
			},
		},
		{
			name: "instance without spot request ID",
			instance: types.Instance{
				InstanceId:       aws.String("i-regular123"),
				PublicIpAddress:  aws.String("198.51.100.42"),
				PrivateIpAddress: aws.String("10.0.3.15"),
				State: &types.InstanceState{
					Name: types.InstanceStateNamePending,
				},
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("my-instance"),
					},
				},
			},
			expected: Machine{
				InstanceID:            "i-regular123",
				PublicIP:              "198.51.100.42",
				PrivateIP:             "10.0.3.15",
				SpotInstanceRequestId: "",
				Status:                "pending",
				Hostname:              "",
			},
		},
		{
			name: "instance with hostname tag among multiple tags",
			instance: types.Instance{
				InstanceId:       aws.String("i-multitag"),
				PrivateIpAddress: aws.String("10.0.4.20"),
				State: &types.InstanceState{
					Name: types.InstanceStateNameRunning,
				},
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("instance-name"),
					},
					{
						Key:   aws.String(tagKeyHostname),
						Value: aws.String("multi.example.com"),
					},
					{
						Key:   aws.String("Environment"),
						Value: aws.String("production"),
					},
				},
			},
			expected: Machine{
				InstanceID:            "i-multitag",
				PublicIP:              "",
				PrivateIP:             "10.0.4.20",
				SpotInstanceRequestId: "",
				Status:                "running",
				Hostname:              "multi.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewMachineFromInstance(tt.instance)

			if result.InstanceID != tt.expected.InstanceID {
				t.Errorf("InstanceID: expected %q, got %q", tt.expected.InstanceID, result.InstanceID)
			}
			if result.PublicIP != tt.expected.PublicIP {
				t.Errorf("PublicIP: expected %q, got %q", tt.expected.PublicIP, result.PublicIP)
			}
			if result.PrivateIP != tt.expected.PrivateIP {
				t.Errorf("PrivateIP: expected %q, got %q", tt.expected.PrivateIP, result.PrivateIP)
			}
			if result.SpotInstanceRequestId != tt.expected.SpotInstanceRequestId {
				t.Errorf("SpotInstanceRequestId: expected %q, got %q", tt.expected.SpotInstanceRequestId, result.SpotInstanceRequestId)
			}
			if result.Status != tt.expected.Status {
				t.Errorf("Status: expected %q, got %q", tt.expected.Status, result.Status)
			}
			if result.Hostname != tt.expected.Hostname {
				t.Errorf("Hostname: expected %q, got %q", tt.expected.Hostname, result.Hostname)
			}
		})
	}
}

func TestMachine_Host(t *testing.T) {
	tests := []struct {
		name     string
		machine  Machine
		expected string
	}{
		{
			name: "returns hostname when present",
			machine: Machine{
				Hostname:  "my-host.example.com",
				PublicIP:  "203.0.113.1",
				PrivateIP: "10.0.1.5",
			},
			expected: "my-host.example.com",
		},
		{
			name: "returns public IP when hostname is empty",
			machine: Machine{
				Hostname:  "",
				PublicIP:  "203.0.113.2",
				PrivateIP: "10.0.1.6",
			},
			expected: "203.0.113.2",
		},
		{
			name: "returns private IP when hostname and public IP are empty",
			machine: Machine{
				Hostname:  "",
				PublicIP:  "",
				PrivateIP: "10.0.1.7",
			},
			expected: "10.0.1.7",
		},
		{
			name: "hostname takes precedence over IPs",
			machine: Machine{
				Hostname:  "priority.example.com",
				PublicIP:  "198.51.100.1",
				PrivateIP: "172.16.0.1",
			},
			expected: "priority.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.machine.Host()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestNewEC2AssumeRolePolicy(t *testing.T) {
	policy := NewEC2AssumeRolePolicy()

	if policy.Version != "2012-10-17" {
		t.Errorf("expected Version %q, got %q", "2012-10-17", policy.Version)
	}

	if len(policy.Statement) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(policy.Statement))
	}

	stmt := policy.Statement[0]
	if stmt.Effect != "Allow" {
		t.Errorf("expected Effect %q, got %q", "Allow", stmt.Effect)
	}

	if stmt.Principal == nil {
		t.Fatal("expected Principal to be non-nil")
	}

	service, ok := stmt.Principal.Service.(string)
	if !ok {
		t.Fatal("expected Principal.Service to be a string")
	}
	if service != "ec2.amazonaws.com" {
		t.Errorf("expected Principal.Service %q, got %q", "ec2.amazonaws.com", service)
	}

	action, ok := stmt.Action.(string)
	if !ok {
		t.Fatal("expected Action to be a string")
	}
	if action != "sts:AssumeRole" {
		t.Errorf("expected Action %q, got %q", "sts:AssumeRole", action)
	}

	// Verify it can be marshaled to JSON
	_, err := json.Marshal(policy)
	if err != nil {
		t.Errorf("failed to marshal policy to JSON: %v", err)
	}
}

func TestNewDevPodEC2Policy(t *testing.T) {
	policy := NewDevPodEC2Policy()

	if policy.Version != "2012-10-17" {
		t.Errorf("expected Version %q, got %q", "2012-10-17", policy.Version)
	}

	if len(policy.Statement) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(policy.Statement))
	}

	// Test Describe statement
	describeStmt := policy.Statement[0]
	if describeStmt.Sid != "Describe" {
		t.Errorf("expected Sid %q, got %q", "Describe", describeStmt.Sid)
	}
	if describeStmt.Effect != "Allow" {
		t.Errorf("expected Effect %q, got %q", "Allow", describeStmt.Effect)
	}

	describeActions, ok := describeStmt.Action.([]string)
	if !ok {
		t.Fatal("expected Action to be []string")
	}
	if len(describeActions) != 1 || describeActions[0] != "ec2:DescribeInstances" {
		t.Errorf("expected Action [\"ec2:DescribeInstances\"], got %v", describeActions)
	}

	describeResource, ok := describeStmt.Resource.(string)
	if !ok {
		t.Fatal("expected Resource to be string")
	}
	if describeResource != "*" {
		t.Errorf("expected Resource %q, got %q", "*", describeResource)
	}

	// Test Stop statement
	stopStmt := policy.Statement[1]
	if stopStmt.Sid != "Stop" {
		t.Errorf("expected Sid %q, got %q", "Stop", stopStmt.Sid)
	}
	if stopStmt.Effect != "Allow" {
		t.Errorf("expected Effect %q, got %q", "Allow", stopStmt.Effect)
	}

	stopActions, ok := stopStmt.Action.([]string)
	if !ok {
		t.Fatal("expected Action to be []string")
	}
	if len(stopActions) != 1 || stopActions[0] != "ec2:StopInstances" {
		t.Errorf("expected Action [\"ec2:StopInstances\"], got %v", stopActions)
	}

	stopResource, ok := stopStmt.Resource.(string)
	if !ok {
		t.Fatal("expected Resource to be string")
	}
	if stopResource != "arn:aws:ec2:*:*:instance/*" {
		t.Errorf("expected Resource %q, got %q", "arn:aws:ec2:*:*:instance/*", stopResource)
	}

	if stopStmt.Condition == nil {
		t.Fatal("expected Condition to be non-nil")
	}

	// Verify it can be marshaled to JSON
	_, err := json.Marshal(policy)
	if err != nil {
		t.Errorf("failed to marshal policy to JSON: %v", err)
	}
}

func TestNewSSMKMSDecryptPolicy(t *testing.T) {
	tests := []struct {
		name   string
		kmsArn string
	}{
		{
			name:   "standard KMS ARN",
			kmsArn: "arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012",
		},
		{
			name:   "different region KMS ARN",
			kmsArn: "arn:aws:kms:eu-west-1:987654321098:key/abcdef12-abcd-abcd-abcd-abcdefabcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := NewSSMKMSDecryptPolicy(tt.kmsArn)

			if policy.Version != "2012-10-17" {
				t.Errorf("expected Version %q, got %q", "2012-10-17", policy.Version)
			}

			if len(policy.Statement) != 1 {
				t.Fatalf("expected 1 statement, got %d", len(policy.Statement))
			}

			stmt := policy.Statement[0]
			if stmt.Sid != "DecryptSSM" {
				t.Errorf("expected Sid %q, got %q", "DecryptSSM", stmt.Sid)
			}
			if stmt.Effect != "Allow" {
				t.Errorf("expected Effect %q, got %q", "Allow", stmt.Effect)
			}

			actions, ok := stmt.Action.([]string)
			if !ok {
				t.Fatal("expected Action to be []string")
			}
			if len(actions) != 1 || actions[0] != "kms:Decrypt" {
				t.Errorf("expected Action [\"kms:Decrypt\"], got %v", actions)
			}

			resource, ok := stmt.Resource.(string)
			if !ok {
				t.Fatal("expected Resource to be string")
			}
			if resource != tt.kmsArn {
				t.Errorf("expected Resource %q, got %q", tt.kmsArn, resource)
			}

			// Verify it can be marshaled to JSON
			_, err := json.Marshal(policy)
			if err != nil {
				t.Errorf("failed to marshal policy to JSON: %v", err)
			}
		})
	}
}

func TestPolicyDocument_JSONMarshaling(t *testing.T) {
	policy := PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:    "TestStatement",
				Effect: "Allow",
				Action: []string{"s3:GetObject", "s3:PutObject"},
				Resource: []string{
					"arn:aws:s3:::my-bucket/*",
					"arn:aws:s3:::other-bucket/*",
				},
			},
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("failed to marshal policy: %v", err)
	}

	// Unmarshal back
	var unmarshaled PolicyDocument
	if err := json.Unmarshal(jsonData, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal policy: %v", err)
	}

	if unmarshaled.Version != policy.Version {
		t.Errorf("Version mismatch: expected %q, got %q", policy.Version, unmarshaled.Version)
	}

	if len(unmarshaled.Statement) != len(policy.Statement) {
		t.Fatalf("Statement count mismatch: expected %d, got %d", len(policy.Statement), len(unmarshaled.Statement))
	}
}

func TestRoute53Zone(t *testing.T) {
	tests := []struct {
		name string
		zone route53Zone
	}{
		{
			name: "public zone",
			zone: route53Zone{
				id:      "Z1234567890ABC",
				Name:    "example.com",
				private: false,
			},
		},
		{
			name: "private zone",
			zone: route53Zone{
				id:      "Z0987654321XYZ",
				Name:    "internal.example.com",
				private: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.zone.id == "" {
				t.Error("zone id should not be empty")
			}
			if tt.zone.Name == "" {
				t.Error("zone Name should not be empty")
			}
		})
	}
}