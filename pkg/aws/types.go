package aws

import "github.com/aws/aws-sdk-go-v2/service/ec2/types"

type Machine struct {
	Status                string
	InstanceID            string
	SpotInstanceRequestId string
	PublicIP              string
	PrivateIP             string
	Hostname              string
}

// NewMachineFromInstance creates a new Machine struct from an AWS ec2 Instance struct.
func NewMachineFromInstance(instance types.Instance) Machine {
	var hostname string
	for _, t := range instance.Tags {
		if *t.Key != tagKeyHostname {
			continue
		}
		hostname = *t.Value
		break
	}

	publicIP := ""
	if instance.PublicIpAddress != nil {
		publicIP = *instance.PublicIpAddress
	}

	spotInstanceRequestID := ""
	if instance.SpotInstanceRequestId != nil {
		spotInstanceRequestID = *instance.SpotInstanceRequestId
	}

	return Machine{
		InstanceID:            *instance.InstanceId,
		Hostname:              hostname,
		PrivateIP:             *instance.PrivateIpAddress,
		PublicIP:              publicIP,
		Status:                string(instance.State.Name),
		SpotInstanceRequestId: spotInstanceRequestID,
	}
}

func (m Machine) Host() string {
	if m.Hostname != "" {
		return m.Hostname
	}
	if m.PublicIP != "" {
		return m.PublicIP
	}
	return m.PrivateIP
}

type route53Zone struct {
	id      string
	Name    string
	private bool
}

// PolicyDocument represents an IAM policy document.
type PolicyDocument struct {
	Version   string            `json:"Version"`
	Statement []PolicyStatement `json:"Statement"`
}

// PolicyStatement represents a statement in an IAM policy.
type PolicyStatement struct {
	Sid       string           `json:"Sid,omitempty"`
	Effect    string           `json:"Effect"`
	Principal *PolicyPrincipal `json:"Principal,omitempty"`
	Action    any              `json:"Action"`             // string or []string
	Resource  any              `json:"Resource,omitempty"` // string or []string
	Condition map[string]any   `json:"Condition,omitempty"`
}

// PolicyPrincipal represents the principal in a policy statement.
type PolicyPrincipal struct {
	Service any `json:"Service,omitempty"` // string or []string
	AWS     any `json:"AWS,omitempty"`     // string or []string
}

// NewEC2AssumeRolePolicy returns the trust policy for EC2 to assume the role.
func NewEC2AssumeRolePolicy() PolicyDocument {
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Effect: "Allow",
				Principal: &PolicyPrincipal{
					Service: "ec2.amazonaws.com",
				},
				Action: "sts:AssumeRole",
			},
		},
	}
}

// NewDevPodEC2Policy returns the policy allowing EC2 instance to describe and stop itself.
func NewDevPodEC2Policy() PolicyDocument {
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:      "Describe",
				Effect:   "Allow",
				Action:   []string{"ec2:DescribeInstances"},
				Resource: "*",
			},
			{
				Sid:      "Stop",
				Effect:   "Allow",
				Action:   []string{"ec2:StopInstances"},
				Resource: "arn:aws:ec2:*:*:instance/*",
				Condition: map[string]any{
					"StringLike": map[string]string{
						"aws:userid": "*:${ec2:InstanceID}",
					},
				},
			},
		},
	}
}

// NewSSMKMSDecryptPolicy returns the policy allowing SSM to decrypt with the specified KMS key.
func NewSSMKMSDecryptPolicy(kmsArn string) PolicyDocument {
	return PolicyDocument{
		Version: "2012-10-17",
		Statement: []PolicyStatement{
			{
				Sid:      "DecryptSSM",
				Effect:   "Allow",
				Action:   []string{"kms:Decrypt"},
				Resource: kmsArn,
			},
		},
	}
}
