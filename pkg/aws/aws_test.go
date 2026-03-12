package aws

import (
	"math"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/skevetter/devpod-provider-aws/pkg/options"
)

func TestValidatedDiskSize(t *testing.T) {
	tests := []struct {
		name      string
		size      int
		expected  int32
		expectErr bool
	}{
		{
			name:      "valid size - small",
			size:      8,
			expected:  8,
			expectErr: false,
		},
		{
			name:      "valid size - medium",
			size:      100,
			expected:  100,
			expectErr: false,
		},
		{
			name:      "valid size - large",
			size:      1000,
			expected:  1000,
			expectErr: false,
		},
		{
			name:      "zero size",
			size:      0,
			expected:  0,
			expectErr: false,
		},
		{
			name:      "negative size",
			size:      -1,
			expectErr: true,
		},
		{
			name:      "exceeds max int32",
			size:      math.MaxInt32 + 1,
			expectErr: true,
		},
		{
			name:      "max valid size",
			size:      math.MaxInt32,
			expected:  math.MaxInt32,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validatedDiskSize(tt.size)

			if tt.expectErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %d, got %d", tt.expected, result)
				}
			}
		})
	}
}

func TestParseCustomTags(t *testing.T) {
	tests := []struct {
		name      string
		tagString string
		expected  []types.Tag
	}{
		{
			name:      "empty string",
			tagString: "",
			expected:  nil,
		},
		{
			name:      "single tag",
			tagString: "Name=Environment,Value=Production",
			expected: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
			},
		},
		{
			name:      "multiple tags",
			tagString: "Name=Environment,Value=Production Name=Team,Value=Engineering",
			expected: []types.Tag{
				{Key: aws.String("Environment"), Value: aws.String("Production")},
				{Key: aws.String("Team"), Value: aws.String("Engineering")},
			},
		},
		{
			name:      "tags with special characters",
			tagString: "Name=app:version,Value=1.0.0 Name=path/name,Value=/usr/local",
			expected: []types.Tag{
				{Key: aws.String("app:version"), Value: aws.String("1.0.0")},
				{Key: aws.String("path/name"), Value: aws.String("/usr/local")},
			},
		},
		{
			name:      "tags with underscores and dashes",
			tagString: "Name=my_tag-name,Value=my_value-123",
			expected: []types.Tag{
				{Key: aws.String("my_tag-name"), Value: aws.String("my_value-123")},
			},
		},
		{
			name:      "invalid format - missing Name",
			tagString: "Value=Test",
			expected:  nil,
		},
		{
			name:      "invalid format - missing Value",
			tagString: "Name=Test",
			expected:  nil,
		},
		{
			name:      "malformed string",
			tagString: "this is not a valid tag",
			expected:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCustomTags(tt.tagString)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d tags, got %d", len(tt.expected), len(result))
			}

			for i := range result {
				if *result[i].Key != *tt.expected[i].Key {
					t.Errorf("tag %d: expected Key %q, got %q", i, *tt.expected[i].Key, *result[i].Key)
				}
				if *result[i].Value != *tt.expected[i].Value {
					t.Errorf("tag %d: expected Value %q, got %q", i, *tt.expected[i].Value, *result[i].Value)
				}
			}
		})
	}
}

func TestBuildBaseTags(t *testing.T) {
	tests := []struct {
		name      string
		machineID string
		zone      route53Zone
		expected  int // expected number of tags
		checkTags func(t *testing.T, tags []types.Tag)
	}{
		{
			name:      "without route53 zone",
			machineID: "test-machine-1",
			zone:      route53Zone{},
			expected:  2,
			checkTags: func(t *testing.T, tags []types.Tag) {
				nameFound := false
				devpodFound := false
				for _, tag := range tags {
					if *tag.Key == "Name" && *tag.Value == "test-machine-1" {
						nameFound = true
					}
					if *tag.Key == tagKeyDevpod && *tag.Value == "test-machine-1" {
						devpodFound = true
					}
				}
				if !nameFound {
					t.Error("Name tag not found or incorrect")
				}
				if !devpodFound {
					t.Error("devpod tag not found or incorrect")
				}
			},
		},
		{
			name:      "with route53 zone",
			machineID: "test-machine-2",
			zone: route53Zone{
				id:   "Z123456789",
				Name: "example.com",
			},
			expected: 3,
			checkTags: func(t *testing.T, tags []types.Tag) {
				hostnameFound := false
				expectedHostname := "test-machine-2.example.com"
				for _, tag := range tags {
					if *tag.Key == tagKeyHostname && *tag.Value == expectedHostname {
						hostnameFound = true
					}
				}
				if !hostnameFound {
					t.Errorf("hostname tag not found or incorrect, expected %q", expectedHostname)
				}
			},
		},
		{
			name:      "with empty zone ID but with name",
			machineID: "test-machine-3",
			zone: route53Zone{
				id:   "",
				Name: "example.org",
			},
			expected: 2,
			checkTags: func(t *testing.T, tags []types.Tag) {
				for _, tag := range tags {
					if *tag.Key == tagKeyHostname {
						t.Error("hostname tag should not be present when zone ID is empty")
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBaseTags(tt.machineID, tt.zone)

			if len(result) != tt.expected {
				t.Errorf("expected %d tags, got %d", tt.expected, len(result))
			}

			tt.checkTags(t, result)
		})
	}
}

func TestSelectSubnetWithMostIPs(t *testing.T) {
	tests := []struct {
		name     string
		subnets  []types.Subnet
		az       string
		expected *types.Subnet
	}{
		{
			name: "select subnet with most IPs - no AZ filter",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(10),
					AvailabilityZone:        aws.String("us-east-1a"),
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(50),
					AvailabilityZone:        aws.String("us-east-1b"),
				},
				{
					SubnetId:                aws.String("subnet-3"),
					AvailableIpAddressCount: aws.Int32(25),
					AvailabilityZone:        aws.String("us-east-1c"),
				},
			},
			az: "",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(50),
				AvailabilityZone:        aws.String("us-east-1b"),
			},
		},
		{
			name: "select subnet with most IPs in specific AZ",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(100),
					AvailabilityZone:        aws.String("us-east-1a"),
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(50),
					AvailabilityZone:        aws.String("us-east-1b"),
				},
				{
					SubnetId:                aws.String("subnet-3"),
					AvailableIpAddressCount: aws.Int32(75),
					AvailabilityZone:        aws.String("us-east-1b"),
				},
			},
			az: "us-east-1b",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-3"),
				AvailableIpAddressCount: aws.Int32(75),
				AvailabilityZone:        aws.String("us-east-1b"),
			},
		},
		{
			name: "no subnets match AZ",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(10),
					AvailabilityZone:        aws.String("us-east-1a"),
				},
			},
			az:       "us-west-2a",
			expected: nil,
		},
		{
			name:     "empty subnet list",
			subnets:  []types.Subnet{},
			az:       "",
			expected: nil,
		},
		{
			name: "subnet without IP count is skipped",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: nil,
					AvailabilityZone:        aws.String("us-east-1a"),
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(30),
					AvailabilityZone:        aws.String("us-east-1a"),
				},
			},
			az: "",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(30),
				AvailabilityZone:        aws.String("us-east-1a"),
			},
		},
		{
			name: "subnet without AZ field is skipped when AZ filter is set",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(100),
					AvailabilityZone:        nil,
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(30),
					AvailabilityZone:        aws.String("us-east-1a"),
				},
			},
			az: "us-east-1a",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(30),
				AvailabilityZone:        aws.String("us-east-1a"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := selectSubnetWithMostIPs(tt.subnets, tt.az)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if *result.SubnetId != *tt.expected.SubnetId {
				t.Errorf("expected SubnetId %q, got %q", *tt.expected.SubnetId, *result.SubnetId)
			}
		})
	}
}

func TestFilterByVPC(t *testing.T) {
	tests := []struct {
		name     string
		subnets  []types.Subnet
		vpcID    string
		expected int
	}{
		{
			name: "filter by specific VPC",
			subnets: []types.Subnet{
				{
					SubnetId: aws.String("subnet-1"),
					VpcId:    aws.String("vpc-123"),
				},
				{
					SubnetId: aws.String("subnet-2"),
					VpcId:    aws.String("vpc-456"),
				},
				{
					SubnetId: aws.String("subnet-3"),
					VpcId:    aws.String("vpc-123"),
				},
			},
			vpcID:    "vpc-123",
			expected: 2,
		},
		{
			name: "no VPC filter returns all",
			subnets: []types.Subnet{
				{
					SubnetId: aws.String("subnet-1"),
					VpcId:    aws.String("vpc-123"),
				},
				{
					SubnetId: aws.String("subnet-2"),
					VpcId:    aws.String("vpc-456"),
				},
			},
			vpcID:    "",
			expected: 2,
		},
		{
			name: "no subnets match VPC",
			subnets: []types.Subnet{
				{
					SubnetId: aws.String("subnet-1"),
					VpcId:    aws.String("vpc-123"),
				},
			},
			vpcID:    "vpc-999",
			expected: 0,
		},
		{
			name:     "empty subnet list",
			subnets:  []types.Subnet{},
			vpcID:    "vpc-123",
			expected: 0,
		},
		{
			name: "subnet with nil VpcId is filtered out",
			subnets: []types.Subnet{
				{
					SubnetId: aws.String("subnet-1"),
					VpcId:    nil,
				},
				{
					SubnetId: aws.String("subnet-2"),
					VpcId:    aws.String("vpc-123"),
				},
			},
			vpcID:    "vpc-123",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterByVPC(tt.subnets, tt.vpcID)

			if len(result) != tt.expected {
				t.Errorf("expected %d subnets, got %d", tt.expected, len(result))
			}

			// Verify all returned subnets have the correct VPC ID
			if tt.vpcID != "" {
				for _, subnet := range result {
					if subnet.VpcId == nil || *subnet.VpcId != tt.vpcID {
						t.Errorf("subnet %s has incorrect VPC ID", *subnet.SubnetId)
					}
				}
			}
		})
	}
}

func TestIsDevpodTagged(t *testing.T) {
	tests := []struct {
		name     string
		tags     []types.Tag
		expected bool
	}{
		{
			name: "has devpod tag",
			tags: []types.Tag{
				{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
			},
			expected: true,
		},
		{
			name: "has devpod tag among others",
			tags: []types.Tag{
				{Key: aws.String("Name"), Value: aws.String("test")},
				{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
				{Key: aws.String("Environment"), Value: aws.String("prod")},
			},
			expected: true,
		},
		{
			name: "no devpod tag",
			tags: []types.Tag{
				{Key: aws.String("Name"), Value: aws.String("test")},
				{Key: aws.String("Environment"), Value: aws.String("prod")},
			},
			expected: false,
		},
		{
			name:     "empty tags",
			tags:     []types.Tag{},
			expected: false,
		},
		{
			name: "devpod key but wrong value",
			tags: []types.Tag{
				{Key: aws.String(tagKeyDevpod), Value: aws.String("wrong-value")},
			},
			expected: false,
		},
		{
			name: "correct value but wrong key",
			tags: []types.Tag{
				{Key: aws.String("other-key"), Value: aws.String(tagKeyDevpod)},
			},
			expected: false,
		},
		{
			name:     "nil tags",
			tags:     nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDevpodTagged(tt.tags)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsPublicSubnetInVPC(t *testing.T) {
	tests := []struct {
		name     string
		subnet   *types.Subnet
		vpcID    string
		expected bool
	}{
		{
			name: "public subnet in correct VPC",
			subnet: &types.Subnet{
				VpcId:                   aws.String("vpc-123"),
				MapPublicIpOnLaunch:     aws.Bool(true),
				AvailableIpAddressCount: aws.Int32(100),
			},
			vpcID:    "vpc-123",
			expected: true,
		},
		{
			name: "private subnet in correct VPC",
			subnet: &types.Subnet{
				VpcId:                   aws.String("vpc-123"),
				MapPublicIpOnLaunch:     aws.Bool(false),
				AvailableIpAddressCount: aws.Int32(100),
			},
			vpcID:    "vpc-123",
			expected: false,
		},
		{
			name: "public subnet in wrong VPC",
			subnet: &types.Subnet{
				VpcId:                   aws.String("vpc-456"),
				MapPublicIpOnLaunch:     aws.Bool(true),
				AvailableIpAddressCount: aws.Int32(100),
			},
			vpcID:    "vpc-123",
			expected: false,
		},
		{
			name: "subnet without VpcId",
			subnet: &types.Subnet{
				VpcId:                   nil,
				MapPublicIpOnLaunch:     aws.Bool(true),
				AvailableIpAddressCount: aws.Int32(100),
			},
			vpcID:    "vpc-123",
			expected: false,
		},
		{
			name: "subnet without MapPublicIpOnLaunch",
			subnet: &types.Subnet{
				VpcId:                   aws.String("vpc-123"),
				MapPublicIpOnLaunch:     nil,
				AvailableIpAddressCount: aws.Int32(100),
			},
			vpcID:    "vpc-123",
			expected: false,
		},
		{
			name: "subnet without AvailableIpAddressCount",
			subnet: &types.Subnet{
				VpcId:                   aws.String("vpc-123"),
				MapPublicIpOnLaunch:     aws.Bool(true),
				AvailableIpAddressCount: nil,
			},
			vpcID:    "vpc-123",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPublicSubnetInVPC(tt.subnet, tt.vpcID)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetInstanceTags(t *testing.T) {
	tests := []struct {
		name         string
		machineID    string
		zone         route53Zone
		instanceTags string
		checkFunc    func(t *testing.T, tagSpecs []types.TagSpecification)
	}{
		{
			name:      "basic tags without custom tags",
			machineID: "test-machine",
			zone:      route53Zone{},
			checkFunc: func(t *testing.T, tagSpecs []types.TagSpecification) {
				if len(tagSpecs) != 1 {
					t.Fatalf("expected 1 tag specification, got %d", len(tagSpecs))
				}
				if tagSpecs[0].ResourceType != "instance" {
					t.Errorf("expected ResourceType 'instance', got %q", tagSpecs[0].ResourceType)
				}
				if len(tagSpecs[0].Tags) < 2 {
					t.Errorf("expected at least 2 base tags, got %d", len(tagSpecs[0].Tags))
				}
			},
		},
		{
			name:      "with route53 zone",
			machineID: "test-machine",
			zone: route53Zone{
				id:   "Z123",
				Name: "example.com",
			},
			checkFunc: func(t *testing.T, tagSpecs []types.TagSpecification) {
				if len(tagSpecs) != 1 {
					t.Fatalf("expected 1 tag specification, got %d", len(tagSpecs))
				}
				hasHostnameTag := false
				for _, tag := range tagSpecs[0].Tags {
					if *tag.Key == tagKeyHostname {
						hasHostnameTag = true
						expectedHostname := "test-machine.example.com"
						if *tag.Value != expectedHostname {
							t.Errorf("expected hostname %q, got %q", expectedHostname, *tag.Value)
						}
					}
				}
				if !hasHostnameTag {
					t.Error("expected hostname tag but didn't find it")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &AwsProvider{
				Config: &options.Options{
					MachineID:    tt.machineID,
					InstanceTags: tt.instanceTags,
				},
			}
			result := GetInstanceTags(provider, tt.zone)
			tt.checkFunc(t, result)
		})
	}
}

func TestSubnetResultFrom(t *testing.T) {
	tests := []struct {
		name     string
		subnet   *types.Subnet
		expected subnetResult
	}{
		{
			name: "subnet with VPC ID",
			subnet: &types.Subnet{
				SubnetId: aws.String("subnet-123"),
				VpcId:    aws.String("vpc-456"),
			},
			expected: subnetResult{
				subnetID: "subnet-123",
				vpcID:    "vpc-456",
			},
		},
		{
			name: "subnet without VPC ID",
			subnet: &types.Subnet{
				SubnetId: aws.String("subnet-789"),
				VpcId:    nil,
			},
			expected: subnetResult{
				subnetID: "subnet-789",
				vpcID:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := subnetResultFrom(tt.subnet)
			if result.subnetID != tt.expected.subnetID {
				t.Errorf("expected subnetID %q, got %q", tt.expected.subnetID, result.subnetID)
			}
			if result.vpcID != tt.expected.vpcID {
				t.Errorf("expected vpcID %q, got %q", tt.expected.vpcID, result.vpcID)
			}
		})
	}
}

func TestAnyState(t *testing.T) {
	states := anyState()

	expectedStates := []string{"pending", "running", "shutting-down", "stopped", "stopping"}

	if len(states) != len(expectedStates) {
		t.Errorf("expected %d states, got %d", len(expectedStates), len(states))
	}

	stateMap := make(map[string]bool)
	for _, s := range states {
		stateMap[s] = true
	}

	for _, expected := range expectedStates {
		if !stateMap[expected] {
			t.Errorf("expected state %q not found", expected)
		}
	}
}

func TestFindTaggedDevPodSubnet(t *testing.T) {
	tests := []struct {
		name     string
		subnets  []types.Subnet
		expected *types.Subnet
	}{
		{
			name: "finds subnet with devpod tag and most IPs",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(50),
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(100),
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
				{
					SubnetId:                aws.String("subnet-3"),
					AvailableIpAddressCount: aws.Int32(30),
					Tags: []types.Tag{
						{Key: aws.String("Name"), Value: aws.String("other")},
					},
				},
			},
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(100),
			},
		},
		{
			name: "no devpod tagged subnets",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(50),
					Tags: []types.Tag{
						{Key: aws.String("Name"), Value: aws.String("test")},
					},
				},
			},
			expected: nil,
		},
		{
			name: "ignores subnets without IP count",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: nil,
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(40),
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
			},
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(40),
			},
		},
		{
			name:     "empty subnet list",
			subnets:  []types.Subnet{},
			expected: nil,
		},
		{
			name: "regression - handles zero available IPs correctly",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					AvailableIpAddressCount: aws.Int32(0),
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
				{
					SubnetId:                aws.String("subnet-2"),
					AvailableIpAddressCount: aws.Int32(10),
					Tags: []types.Tag{
						{Key: aws.String(tagKeyDevpod), Value: aws.String(tagKeyDevpod)},
					},
				},
			},
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(10),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findTaggedDevPodSubnet(tt.subnets)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got subnet %s", *result.SubnetId)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if *result.SubnetId != *tt.expected.SubnetId {
				t.Errorf("expected SubnetId %q, got %q", *tt.expected.SubnetId, *result.SubnetId)
			}

			if *result.AvailableIpAddressCount != *tt.expected.AvailableIpAddressCount {
				t.Errorf("expected AvailableIpAddressCount %d, got %d",
					*tt.expected.AvailableIpAddressCount,
					*result.AvailableIpAddressCount)
			}
		})
	}
}

func TestFindVPCPublicSubnet(t *testing.T) {
	tests := []struct {
		name     string
		subnets  []types.Subnet
		vpcID    string
		expected *types.Subnet
	}{
		{
			name: "finds public subnet in VPC with most IPs",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					VpcId:                   aws.String("vpc-123"),
					MapPublicIpOnLaunch:     aws.Bool(true),
					AvailableIpAddressCount: aws.Int32(50),
				},
				{
					SubnetId:                aws.String("subnet-2"),
					VpcId:                   aws.String("vpc-123"),
					MapPublicIpOnLaunch:     aws.Bool(true),
					AvailableIpAddressCount: aws.Int32(100),
				},
			},
			vpcID: "vpc-123",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(100),
			},
		},
		{
			name: "returns nil for empty VPC ID",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					VpcId:                   aws.String("vpc-123"),
					MapPublicIpOnLaunch:     aws.Bool(true),
					AvailableIpAddressCount: aws.Int32(50),
				},
			},
			vpcID:    "",
			expected: nil,
		},
		{
			name: "ignores private subnets",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					VpcId:                   aws.String("vpc-123"),
					MapPublicIpOnLaunch:     aws.Bool(false),
					AvailableIpAddressCount: aws.Int32(100),
				},
				{
					SubnetId:                aws.String("subnet-2"),
					VpcId:                   aws.String("vpc-123"),
					MapPublicIpOnLaunch:     aws.Bool(true),
					AvailableIpAddressCount: aws.Int32(30),
				},
			},
			vpcID: "vpc-123",
			expected: &types.Subnet{
				SubnetId:                aws.String("subnet-2"),
				AvailableIpAddressCount: aws.Int32(30),
			},
		},
		{
			name: "no matching public subnets in VPC",
			subnets: []types.Subnet{
				{
					SubnetId:                aws.String("subnet-1"),
					VpcId:                   aws.String("vpc-456"),
					MapPublicIpOnLaunch:     aws.Bool(true),
					AvailableIpAddressCount: aws.Int32(50),
				},
			},
			vpcID:    "vpc-123",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findVPCPublicSubnet(tt.subnets, tt.vpcID)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got subnet %s", *result.SubnetId)
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if *result.SubnetId != *tt.expected.SubnetId {
				t.Errorf("expected SubnetId %q, got %q", *tt.expected.SubnetId, *result.SubnetId)
			}
		})
	}
}