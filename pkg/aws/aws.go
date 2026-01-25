package aws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/sirupsen/logrus"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/skevetter/devpod-provider-aws/pkg/options"
	"github.com/skevetter/devpod/pkg/client"
	"github.com/skevetter/devpod/pkg/ssh"
	"github.com/skevetter/log"
)

const (
	tagKeyHostname             = "devpod:hostname"
	devpodIAMResourceName      = "devpod-ec2-role"
	iamEC2PolicyName           = "devpod-ec2-policy"
	iamSSMKMSDecryptPolicyName = "ssm-kms-decrypt-policy"
)

// detect if we're in an ec2 instance
func isEC2Instance() bool {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "http://instance-data.ec2.internal", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return true
}

func NewProvider(ctx context.Context, withFolder bool, log log.Logger) (*AwsProvider, error) {
	log.Debugf("creating new AWS provider")
	config, err := options.FromEnv(false, withFolder)
	if err != nil {
		return nil, err
	}

	cfg, err := NewAWSConfig(ctx, log, config)
	if err != nil {
		return nil, err
	}

	if err := logCallerIdentity(ctx, cfg, log); err != nil {
		log.Warnf("failed to get caller identity: %v", err)
	}

	if err := configureDefaults(ctx, cfg, config, log); err != nil {
		return nil, err
	}

	accountID := getCallerAccount(ctx, cfg)

	provider := &AwsProvider{
		Config:    config,
		AwsConfig: cfg,
		Log:       log,
		accountID: accountID,
	}

	log.Debugf("AWS provider created")
	return provider, nil
}

func configureDefaults(ctx context.Context, cfg aws.Config, config *options.Options, log log.Logger) error {
	isEC2 := isEC2Instance()
	log.Debugf("running in EC2 instance: %v", isEC2)

	if config.DiskImage == "" && !isEC2 {
		if err := setDefaultAMI(ctx, cfg, config, log); err != nil {
			return err
		}
	}

	if config.RootDevice == "" && !isEC2 && config.DiskImage != "" {
		setRootDevice(ctx, cfg, config, log)
	}

	if config.RootDevice == "" {
		config.RootDevice = "/dev/sda1"
	}

	return nil
}

func setDefaultAMI(ctx context.Context, cfg aws.Config, config *options.Options, log log.Logger) error {
	log.Debugf("disk image not specified; fetching default AMI for instance type: %s", config.MachineType)
	image, err := GetDefaultAMI(ctx, cfg, config.MachineType)
	if err != nil {
		return err
	}
	log.Debugf("using default AMI: %s", image)
	config.DiskImage = image
	return nil
}

func setRootDevice(ctx context.Context, cfg aws.Config, config *options.Options, log log.Logger) {
	log.Debugf("determining root device for AMI: %s", config.DiskImage)
	device, err := GetAMIRootDevice(ctx, cfg, config.DiskImage)
	if err != nil {
		log.Debugf("could not determine root device for AMI %s: %v, using default /dev/sda1", config.DiskImage, err)
		config.RootDevice = "/dev/sda1"
	} else {
		log.Debugf("using root device: %s", device)
		config.RootDevice = device
	}
}

func NewAWSConfig(ctx context.Context, log log.Logger, options *options.Options) (aws.Config, error) {
	log.Debugf("configuring AWS SDK for region: %s", options.Zone)
	opts := buildConfigOptions(ctx, log, options)
	cfg, err := awsConfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	log.Debugf("AWS SDK configured")
	return cfg, nil
}

func buildConfigOptions(ctx context.Context, log log.Logger, options *options.Options) []func(*awsConfig.LoadOptions) error {
	var opts []func(*awsConfig.LoadOptions) error

	if options.Zone != "" {
		opts = append(opts, awsConfig.WithRegion(options.Zone))
	}

	if options.AccessKeyID != "" && options.SecretAccessKey != "" {
		log.Debugf("using provided AWS credentials")
		opts = append(opts, awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     options.AccessKeyID,
				SecretAccessKey: options.SecretAccessKey,
				SessionToken:    options.SessionToken,
			},
		}))
	} else if options.CustomCredentialCommand != "" {
		creds, err := executeCredentialCommand(ctx, options.CustomCredentialCommand, log)
		if err != nil {
			log.Errorf("custom credential command failed: %v", err)
		}
		opts = append(opts, awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{Value: creds}))
	} else {
		profile := os.Getenv("AWS_PROFILE")
		if profile != "" {
			log.Debugf("using AWS profile: %s", profile)
		} else {
			log.Debugf("using default AWS credential chain")
		}
	}

	return opts
}

func executeCredentialCommand(ctx context.Context, command string, log log.Logger) (aws.Credentials, error) {
	log.Debugf("using custom credential command: %s", command)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdout = &output
	cmd.Stderr = log.Writer(logrus.ErrorLevel, true)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return aws.Credentials{}, fmt.Errorf("credential command timed out after 30s")
		}
		return aws.Credentials{}, fmt.Errorf("run command %q: %w", command, err)
	}

	var creds aws.Credentials
	if err := json.Unmarshal(output.Bytes(), &creds); err != nil {
		return aws.Credentials{}, fmt.Errorf("parse AWS credential JSON output: %w", err)
	}

	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return aws.Credentials{}, fmt.Errorf("custom credential command output missing required fields")
	}

	return creds, nil
}

type AwsProvider struct {
	Config           *options.Options
	AwsConfig        aws.Config
	Log              log.Logger
	WorkingDirectory string
	accountID        string
}

func GetSubnet(ctx context.Context, provider *AwsProvider) (string, error) {
	provider.Log.Debugf("GetSubnet: vpc=%s az=%s subnets=%v",
		provider.Config.VpcID, provider.Config.AvailabilityZone, provider.Config.SubnetIDs)

	if len(provider.Config.SubnetIDs) == 1 {
		provider.Log.Debugf("GetSubnet: using configured subnet %s", provider.Config.SubnetIDs[0])
		return provider.Config.SubnetIDs[0], nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)

	if len(provider.Config.SubnetIDs) > 1 {
		return selectFromSpecifiedSubnets(ctx, svc, provider.Config.SubnetIDs, provider.Config.AvailabilityZone, provider.Log)
	}

	return discoverSubnet(ctx, svc, provider.Config.VpcID, provider.Config.AvailabilityZone, provider.Log)
}

func selectFromSpecifiedSubnets(ctx context.Context, svc *ec2.Client, subnetIDs []string, az string, log log.Logger) (string, error) {
	log.Debugf("selecting subnet from %d specified subnets", len(subnetIDs))
	subnets, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: subnetIDs})
	if err != nil {
		return "", fmt.Errorf("list specified subnets %q: %w", subnetIDs, err)
	}
	if len(subnets.Subnets) == 0 {
		return "", fmt.Errorf("no subnets found with IDs %q", subnetIDs)
	}

	subnet := selectSubnetWithMostIPs(subnets.Subnets, az)
	if subnet == nil {
		if az == "" {
			return "", fmt.Errorf("no subnets found with IDs %q", subnetIDs)
		}
		return "", fmt.Errorf("no subnets found with IDs %q in availability zone %q", subnetIDs, az)
	}

	log.Debugf("selected subnet %s with %d available IPs", *subnet.SubnetId, *subnet.AvailableIpAddressCount)
	return *subnet.SubnetId, nil
}

func selectSubnetWithMostIPs(subnets []types.Subnet, az string) *types.Subnet {
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := subnets[i]
		if az != "" {
			if s.AvailabilityZone == nil || *s.AvailabilityZone != az {
				continue
			}
		}
		if s.AvailableIpAddressCount == nil {
			continue
		}
		if selected == nil || *s.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *s.AvailableIpAddressCount
			selected = &subnets[i]
		}
	}
	return selected
}

func discoverSubnet(ctx context.Context, svc *ec2.Client, vpcID, az string, log log.Logger) (string, error) {
	log.Debugf("searching for suitable subnet")
	subnets, err := listAllSubnets(ctx, svc, az)
	if err != nil {
		return "", err
	}

	if subnet := findTaggedDevPodSubnet(subnets); subnet != nil {
		log.Debugf("found tagged subnet %s with %d available IPs", *subnet.SubnetId, *subnet.AvailableIpAddressCount)
		return *subnet.SubnetId, nil
	}

	if subnet := findVPCPublicSubnet(subnets, vpcID); subnet != nil {
		log.Debugf("found VPC subnet %s with %d available IPs", *subnet.SubnetId, *subnet.AvailableIpAddressCount)
		return *subnet.SubnetId, nil
	}

	if vpcID == "" {
		return "", fmt.Errorf("could not find a suitable subnet. Please either specify a subnet ID or VPC ID, or tag the desired subnets with devpod=devpod")
	}

	return "", fmt.Errorf("no suitable subnet found in VPC %q. Please specify a subnet ID or tag subnets with devpod=devpod", vpcID)
}

func listAllSubnets(ctx context.Context, svc *ec2.Client, az string) ([]types.Subnet, error) {
	input := &ec2.DescribeSubnetsInput{}
	if az != "" {
		input.Filters = []types.Filter{{Name: aws.String("availability-zone"), Values: []string{az}}}
	}

	var subnets []types.Subnet
	p := ec2.NewDescribeSubnetsPaginator(svc, input)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list all subnets: %w", err)
		}
		subnets = append(subnets, page.Subnets...)
	}
	return subnets, nil
}

func findTaggedDevPodSubnet(subnets []types.Subnet) *types.Subnet {
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := subnets[i]
		if s.AvailableIpAddressCount == nil {
			continue
		}
		for _, tag := range s.Tags {
			if tag.Key == nil || tag.Value == nil {
				continue
			}
			if *tag.Key == "devpod" && *tag.Value == "devpod" {
				if selected == nil || *s.AvailableIpAddressCount > maxIPCount {
					maxIPCount = *s.AvailableIpAddressCount
					selected = &subnets[i]
				}
				break
			}
		}
	}
	return selected
}

func findVPCPublicSubnet(subnets []types.Subnet, vpcID string) *types.Subnet {
	if vpcID == "" {
		return nil
	}
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := &subnets[i]
		if s.VpcId == nil || s.MapPublicIpOnLaunch == nil || s.AvailableIpAddressCount == nil {
			continue
		}
		if *s.VpcId == vpcID && *s.MapPublicIpOnLaunch && *s.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *s.AvailableIpAddressCount
			selected = s
		}
	}
	return selected
}

func GetDevpodVPC(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.VpcID != "" {
		provider.Log.Debugf("GetDevpodVPC: using VPC %s", provider.Config.VpcID)
		return provider.Config.VpcID, nil
	}

	// Get a list of VPCs so we can associate the group with the first VPC.
	svc := ec2.NewFromConfig(provider.AwsConfig)

	result, err := svc.DescribeVpcs(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("describe VPCs: %w", err)
	}

	if len(result.Vpcs) == 0 {
		return "", fmt.Errorf("there are no VPCs to associate with")
	}

	// We need to find a default vpc
	for _, vpc := range result.Vpcs {
		if *vpc.IsDefault {
			provider.Log.Debugf("GetDevpodVPC: using VPC %s", *vpc.VpcId)
			return *vpc.VpcId, nil
		}
	}

	return "", nil
}

func GetDefaultAMI(ctx context.Context, cfg aws.Config, instanceType string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	architecture := "amd64"
	if strings.HasSuffix(strings.Split(instanceType, ".")[0], "g") {
		architecture = "arm64"
	}

	// Try Ubuntu 24.04 LTS (Noble) first, then fall back to 22.04 LTS (Jammy)
	patterns := []string{
		fmt.Sprintf("ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd/ubuntu-noble-24.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd-gp3/ubuntu-jammy-22.04-%s-server-*", architecture),
		fmt.Sprintf("ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-%s-server-*", architecture),
	}

	for _, pattern := range patterns {
		input := &ec2.DescribeImagesInput{
			Owners: []string{"099720109477"}, // Canonical
			Filters: []types.Filter{
				{
					Name:   aws.String("name"),
					Values: []string{pattern},
				},
				{
					Name:   aws.String("state"),
					Values: []string{"available"},
				},
				{
					Name:   aws.String("root-device-type"),
					Values: []string{"ebs"},
				},
			},
		}

		result, err := svc.DescribeImages(ctx, input)
		if err != nil {
			return "", err
		}

		if len(result.Images) == 0 {
			continue
		}

		// Sort by creation date to get the latest
		sort.Slice(result.Images, func(i, j int) bool {
			iTime, _ := time.Parse("2006-01-02T15:04:05.000Z", *result.Images[i].CreationDate)
			jTime, _ := time.Parse("2006-01-02T15:04:05.000Z", *result.Images[j].CreationDate)
			return iTime.After(jTime)
		})

		return *result.Images[0].ImageId, nil
	}

	return "", fmt.Errorf("no matching Ubuntu LTS AMI found for architecture %s", architecture)
}

func GetAMIRootDevice(ctx context.Context, cfg aws.Config, diskImage string) (string, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeImagesInput{
		ImageIds: []string{
			diskImage,
		},
	}
	result, err := svc.DescribeImages(ctx, input)
	if err != nil {
		return "", err
	}

	// Struct spec: https://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#Image
	if len(result.Images) == 0 || *result.Images[0].RootDeviceName == "" {
		return "/dev/sda1", nil
	}

	return *result.Images[0].RootDeviceName, nil
}

func GetDevpodInstanceProfile(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.InstanceProfileArn != "" {
		provider.Log.Debugf("GetDevpodInstanceProfile: using profile %s", provider.Config.InstanceProfileArn)
		return provider.Config.InstanceProfileArn, nil
	}

	svc := iam.NewFromConfig(provider.AwsConfig)

	roleInput := &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(devpodIAMResourceName),
	}

	response, err := svc.GetInstanceProfile(ctx, roleInput)
	if err != nil {
		return CreateDevpodInstanceProfile(ctx, provider)
	}

	provider.Log.Debugf("GetDevpodInstanceProfile: using existing profile %s", *response.InstanceProfile.Arn)
	return *response.InstanceProfile.Arn, nil
}

func CreateDevpodInstanceProfile(ctx context.Context, provider *AwsProvider) (string, error) {
	svc := iam.NewFromConfig(provider.AwsConfig)

	if err := createIAMRole(ctx, svc); err != nil {
		return "", err
	}

	if err := attachRolePolicies(ctx, svc, provider.Config.KmsKeyARNForSessionManager); err != nil {
		return "", err
	}

	return createInstanceProfile(ctx, svc)
}

func createIAMRole(ctx context.Context, svc *iam.Client) error {
	assumeRolePolicy := NewEC2AssumeRolePolicy()
	assumeRolePolicyJSON, err := json.Marshal(assumeRolePolicy)
	if err != nil {
		return fmt.Errorf("marshal assume role policy: %w", err)
	}

	_, err = svc.CreateRole(ctx, &iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(string(assumeRolePolicyJSON)),
		RoleName:                 aws.String(devpodIAMResourceName),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if errors.As(err, &exists) {
			return nil
		}
		return fmt.Errorf("create role: %w", err)
	}
	return nil
}

func attachRolePolicies(ctx context.Context, svc *iam.Client, kmsArn string) error {
	ec2Policy := NewDevPodEC2Policy()
	ec2PolicyJSON, err := json.Marshal(ec2Policy)
	if err != nil {
		return fmt.Errorf("marshal EC2 policy: %w", err)
	}

	if _, err = svc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		PolicyDocument: aws.String(string(ec2PolicyJSON)),
		PolicyName:     aws.String(iamEC2PolicyName),
		RoleName:       aws.String(devpodIAMResourceName),
	}); err != nil {
		return fmt.Errorf("put role policy: %w", err)
	}

	if _, err = svc.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
		RoleName:  aws.String(devpodIAMResourceName),
	}); err != nil {
		return fmt.Errorf("attach SSM policy: %w", err)
	}

	if kmsArn != "" {
		kmsPolicy := NewSSMKMSDecryptPolicy(kmsArn)
		kmsPolicyJSON, err := json.Marshal(kmsPolicy)
		if err != nil {
			return fmt.Errorf("marshal KMS policy: %w", err)
		}

		if _, err = svc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			PolicyDocument: aws.String(string(kmsPolicyJSON)),
			PolicyName:     aws.String(iamSSMKMSDecryptPolicyName),
			RoleName:       aws.String(devpodIAMResourceName),
		}); err != nil {
			return fmt.Errorf("put KMS decrypt policy: %w", err)
		}
	}

	return nil
}

func createInstanceProfile(ctx context.Context, svc *iam.Client) (string, error) {
	arn, err := createOrGetInstanceProfile(ctx, svc)
	if err != nil {
		return "", err
	}

	if err := attachRoleToProfile(ctx, svc); err != nil {
		return "", err
	}

	if err := waitForInstanceProfile(ctx, svc); err != nil {
		return "", err
	}

	return arn, nil
}

func createOrGetInstanceProfile(ctx context.Context, svc *iam.Client) (string, error) {
	response, err := svc.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(devpodIAMResourceName),
	})
	if err != nil {
		var exists *iamtypes.EntityAlreadyExistsException
		if errors.As(err, &exists) {
			getResponse, err := svc.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
				InstanceProfileName: aws.String(devpodIAMResourceName),
			})
			if err != nil {
				return "", fmt.Errorf("get instance profile: %w", err)
			}
			return *getResponse.InstanceProfile.Arn, nil
		}
		return "", fmt.Errorf("create instance profile: %w", err)
	}
	return *response.InstanceProfile.Arn, nil
}

func attachRoleToProfile(ctx context.Context, svc *iam.Client) error {
	_, err := svc.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(devpodIAMResourceName),
		RoleName:            aws.String(devpodIAMResourceName),
	})
	if err != nil {
		var already *iamtypes.EntityAlreadyExistsException
		if !errors.As(err, &already) {
			return fmt.Errorf("add role to instance profile: %w", err)
		}
	}
	return nil
}

func waitForInstanceProfile(ctx context.Context, svc *iam.Client) error {
	waiter := iam.NewInstanceProfileExistsWaiter(svc)
	if err := waiter.Wait(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: aws.String(devpodIAMResourceName),
	}, 2*time.Minute); err != nil {
		return fmt.Errorf("wait for instance profile: %w", err)
	}
	return nil
}

func GetDevpodSecurityGroups(ctx context.Context, provider *AwsProvider) ([]string, error) {
	if provider.Config.SecurityGroupID != "" {
		sgs := strings.Split(provider.Config.SecurityGroupID, ",")
		provider.Log.Debugf("GetDevpodSecurityGroups: using configured groups %v", sgs)
		return sgs, nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)
	input := &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					"devpod",
				},
			},
		},
	}

	if provider.Config.VpcID != "" {
		input.Filters = append(input.Filters, types.Filter{
			Name: aws.String("vpc-id"),
			Values: []string{
				provider.Config.VpcID,
			},
		})
	}

	result, err := svc.DescribeSecurityGroups(ctx, input)
	// It it is not created, do it
	if result == nil || len(result.SecurityGroups) == 0 || err != nil {
		sg, err := CreateDevpodSecurityGroup(ctx, provider)
		if err != nil {
			return nil, err
		}

		provider.Log.Debugf("GetDevpodSecurityGroups: created new group %s", sg)
		return []string{sg}, nil
	}

	sgs := []string{}
	for res := range result.SecurityGroups {
		sgs = append(sgs, *result.SecurityGroups[res].GroupId)
	}

	provider.Log.Debugf("GetDevpodSecurityGroups: using existing groups %v", sgs)
	return sgs, nil
}

func CreateDevpodSecurityGroup(ctx context.Context, provider *AwsProvider) (string, error) {
	var err error

	svc := ec2.NewFromConfig(provider.AwsConfig)

	vpc, err := GetDevpodVPC(ctx, provider)
	if err != nil {
		return "", err
	}

	// Create the security group with the VPC, name, and description.
	result, err := svc.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("devpod"),
		Description: aws.String("Default Security Group for DevPod"),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group",
				Tags: []types.Tag{
					{
						Key:   aws.String("devpod"),
						Value: aws.String("devpod"),
					},
				},
			},
		},
		VpcId: aws.String(vpc),
	})
	if err != nil {
		return "", err
	}

	groupID := *result.GroupId

	// No need to open ssh port if use session manager.
	if provider.Config.UseSessionManager {
		return groupID, nil
	}

	// Add permissions to the security group
	_, err = svc.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(22),
				ToPort:     aws.Int32(22),
				IpRanges: []types.IpRange{
					{
						CidrIp: aws.String("0.0.0.0/0"),
					},
				},
			},
		},
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group-rule",
				Tags: []types.Tag{
					{
						Key:   aws.String("devpod"),
						Value: aws.String("devpod-ingress"),
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}

	return groupID, nil
}

func GetDevpodInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					name,
				},
			},
			{
				Name: aws.String("instance-state-name"),
				Values: []string{
					"pending",
					"running",
					"shutting-down",
					"stopped",
					"stopping",
				},
			},
		},
	}

	result, err := svc.DescribeInstances(ctx, input)
	if err != nil {
		return Machine{}, err
	}

	// Sort slice in order to have the newest result first
	sort.Slice(result.Reservations, func(i, j int) bool {
		return result.Reservations[i].Instances[0].LaunchTime.After(
			*result.Reservations[j].Instances[0].LaunchTime,
		)
	})

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return Machine{}, nil
	}
	return NewMachineFromInstance(result.Reservations[0].Instances[0]), nil
}

func GetDevpodStoppedInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					name,
				},
			},
			{
				Name: aws.String("instance-state-name"),
				Values: []string{
					"stopped",
				},
			},
		},
	}

	result, err := svc.DescribeInstances(ctx, input)
	if err != nil {
		return Machine{}, err
	}

	// Sort slice in order to have the newest result first
	sort.Slice(result.Reservations, func(i, j int) bool {
		return result.Reservations[i].Instances[0].LaunchTime.After(
			*result.Reservations[j].Instances[0].LaunchTime,
		)
	})

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return Machine{}, nil
	}
	return NewMachineFromInstance(result.Reservations[0].Instances[0]), nil
}

func GetDevpodRunningInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	svc := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					name,
				},
			},
			{
				Name: aws.String("instance-state-name"),
				Values: []string{
					"running",
				},
			},
		},
	}

	result, err := svc.DescribeInstances(ctx, input)
	if err != nil {
		return Machine{}, err
	}

	// Sort slice in order to have the newest result first
	sort.Slice(result.Reservations, func(i, j int) bool {
		return result.Reservations[i].Instances[0].LaunchTime.After(
			*result.Reservations[j].Instances[0].LaunchTime,
		)
	})

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return Machine{}, nil
	}
	return NewMachineFromInstance(result.Reservations[0].Instances[0]), nil
}

func GetInstanceTags(providerAws *AwsProvider, zone route53Zone) []types.TagSpecification {
	tags := buildBaseTags(providerAws.Config.MachineID, zone)
	customTags := parseCustomTags(providerAws.Config.InstanceTags)
	tags = append(tags, customTags...)

	return []types.TagSpecification{{ResourceType: "instance", Tags: tags}}
}

func buildBaseTags(machineID string, zone route53Zone) []types.Tag {
	tags := []types.Tag{
		{Key: aws.String("Name"), Value: aws.String(machineID)},
		{Key: aws.String("devpod"), Value: aws.String(machineID)},
	}

	if zone.id != "" {
		tags = append(tags, types.Tag{
			Key:   aws.String(tagKeyHostname),
			Value: aws.String(machineID + "." + zone.Name),
		})
	}

	return tags
}

func parseCustomTags(tagString string) []types.Tag {
	if tagString == "" {
		return nil
	}

	reg := regexp.MustCompile(`Name=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+),Value=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+)`)
	tagList := reg.FindAllString(tagString, -1)
	if tagList == nil {
		return nil
	}

	tags := make([]types.Tag, 0, len(tagList))
	for _, tag := range tagList {
		tagSplit := strings.Split(tag, ",")
		name := strings.ReplaceAll(tagSplit[0], "Name=", "")
		value := strings.ReplaceAll(tagSplit[1], "Value=", "")
		tags = append(tags, types.Tag{Key: aws.String(name), Value: aws.String(value)})
	}

	return tags
}

func Create(
	ctx context.Context,
	cfg aws.Config,
	providerAws *AwsProvider,
) (Machine, error) {
	providerAws.Log.Debugf("Create: machine=%s type=%s ami=%s disk=%dGB",
		providerAws.Config.MachineID,
		providerAws.Config.MachineType,
		providerAws.Config.DiskImage,
		providerAws.Config.DiskSizeGB,
	)

	svc := ec2.NewFromConfig(cfg)

	providerAws.Log.Debugf("getting security groups")
	devpodSG, err := GetDevpodSecurityGroups(ctx, providerAws)
	if err != nil {
		return Machine{}, fmt.Errorf("get security groups: %w", err)
	}
	providerAws.Log.Debugf("using security groups: %v", devpodSG)

	volSizeI32 := int32(providerAws.Config.DiskSizeGB)

	providerAws.Log.Debugf("generating user data script")
	userData, err := GetInjectKeypairScript(providerAws.Config.MachineFolder)
	if err != nil {
		return Machine{}, err
	}

	var r53Zone route53Zone
	if providerAws.Config.UseRoute53Hostnames {
		providerAws.Log.Debugf("Route53 hostnames enabled, getting zone")
		r53Zone, err = GetDevpodRoute53Zone(ctx, providerAws)
		if err != nil {
			return Machine{}, err
		}
		providerAws.Log.Debugf("using Route53 zone: %s (ID: %s)", r53Zone.Name, r53Zone.id)
	}

	instance := &ec2.RunInstancesInput{
		ImageId:          aws.String(providerAws.Config.DiskImage),
		InstanceType:     types.InstanceType(providerAws.Config.MachineType),
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SecurityGroupIds: devpodSG,
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpEndpoint:            types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(1),
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String(providerAws.Config.RootDevice),
				Ebs: &types.EbsBlockDevice{
					VolumeSize: &volSizeI32,
				},
			},
		},
		TagSpecifications: GetInstanceTags(providerAws, r53Zone),
		UserData:          &userData,
	}
	if providerAws.Config.UseSpotInstance {
		providerAws.Log.Debugf("using spot instance")
		instance.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
			MarketType: "spot",
			SpotOptions: &types.SpotMarketOptions{
				SpotInstanceType:             "persistent",
				InstanceInterruptionBehavior: "stop",
			},
		}
	}

	providerAws.Log.Debugf("getting instance profile")
	profile, err := GetDevpodInstanceProfile(ctx, providerAws)
	if err == nil {
		providerAws.Log.Debugf("using instance profile: %s", profile)
		instance.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Arn: aws.String(profile),
		}
	} else {
		providerAws.Log.Warnf("failed to get instance profile: %v", err)
	}

	subnetID, err := GetSubnet(ctx, providerAws)
	if err != nil {
		return Machine{}, fmt.Errorf("determine subnet ID: %w", err)
	}
	providerAws.Log.Debugf("using subnet: %s", subnetID)
	instance.SubnetId = &subnetID

	providerAws.Log.Debugf("launching EC2 instance")
	result, err := svc.RunInstances(ctx, instance)
	if err != nil {
		return Machine{}, err
	}
	providerAws.Log.Debugf("EC2 instance launched: %s", *result.Instances[0].InstanceId)

	if r53Zone.id != "" {
		hostname := providerAws.Config.MachineID + "." + r53Zone.Name
		providerAws.Log.Debugf("creating Route53 record: %s -> %s", hostname, *result.Instances[0].PrivateIpAddress)
		if err := UpsertDevpodRoute53Record(ctx, providerAws, r53Zone.id, hostname, *result.Instances[0].PrivateIpAddress); err != nil {
			return Machine{}, fmt.Errorf("create Route53 record: %w", err)
		}
	}

	machine := NewMachineFromInstance(result.Instances[0])
	providerAws.Log.Debugf("Create: instance %s created", machine.InstanceID)
	return machine, nil
}

func Start(ctx context.Context, provider *AwsProvider, instanceID string) error {
	provider.Log.Debugf("Start: instance=%s", instanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.StartInstancesInput{
		InstanceIds: []string{
			instanceID,
		},
	}

	_, err := svc.StartInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	provider.Log.Debugf("Start: instance %s started", instanceID)
	return nil
}

func Stop(ctx context.Context, provider *AwsProvider, instanceID string) error {
	provider.Log.Debugf("Stop: instance=%s", instanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.StopInstancesInput{
		InstanceIds: []string{
			instanceID,
		},
	}

	_, err := svc.StopInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	provider.Log.Debugf("Stop: instance %s stopped", instanceID)
	return nil
}

func Status(ctx context.Context, provider *AwsProvider, name string) (client.Status, error) {
	provider.Log.Debugf("Status: machine=%s", name)

	result, err := GetDevpodInstance(ctx, provider.AwsConfig, name)
	if err != nil {
		return client.StatusNotFound, fmt.Errorf("get instance: %w", err)
	}

	if result.Status == "" {
		provider.Log.Debugf("Status: machine %s not found", name)
		return client.StatusNotFound, nil
	}

	status := result.Status
	var clientStatus client.Status
	switch status {
	case "running":
		clientStatus = client.StatusRunning
	case "stopped":
		clientStatus = client.StatusStopped
	case "terminated":
		provider.Log.Debugf("Status: machine %s terminated", name)
		return client.StatusNotFound, nil
	default:
		clientStatus = client.StatusBusy
	}

	provider.Log.Debugf("Status: machine %s is %s", name, status)
	return clientStatus, nil
}

func Delete(ctx context.Context, provider *AwsProvider, machine Machine) error {
	provider.Log.Debugf("Delete: instance=%s", machine.InstanceID)

	svc := ec2.NewFromConfig(provider.AwsConfig)

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{
			machine.InstanceID,
		},
	}

	_, err := svc.TerminateInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("terminate instance: %w", err)
	}

	if machine.SpotInstanceRequestId != "" {
		_, err = svc.CancelSpotInstanceRequests(ctx, &ec2.CancelSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{
				machine.SpotInstanceRequestId,
			},
		})
		if err != nil {
			return fmt.Errorf("cancel spot request: %w", err)
		}
	}

	if provider.Config.UseRoute53Hostnames {
		r53Zone, err := GetDevpodRoute53Zone(ctx, provider)
		if err != nil {
			return fmt.Errorf("get Route53 zone: %w", err)
		}
		if r53Zone.id != "" {
			if err := DeleteDevpodRoute53Record(ctx, provider, r53Zone, machine); err != nil {
				return fmt.Errorf("delete Route53 record: %w", err)
			}
		}
	}

	provider.Log.Debugf("Delete: instance %s terminated", machine.InstanceID)
	return nil
}

func GetInjectKeypairScript(dir string) (string, error) {
	publicKeyBase, err := ssh.GetPublicKeyBase(dir)
	if err != nil {
		return "", err
	}

	publicKey, err := base64.StdEncoding.DecodeString(publicKeyBase)
	if err != nil {
		return "", err
	}

	resultScript := `#!/bin/sh
useradd devpod -d /home/devpod
mkdir -p /home/devpod
if grep -q sudo /etc/groups; then
	usermod -aG sudo devpod
elif grep -q wheel /etc/groups; then
	usermod -aG wheel devpod
fi
echo "devpod ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/91-devpod
mkdir -p /home/devpod/.ssh
echo "` + string(publicKey) + `" >> /home/devpod/.ssh/authorized_keys
chmod 0700 /home/devpod/.ssh
chmod 0600 /home/devpod/.ssh/authorized_keys
chown -R devpod:devpod /home/devpod`

	return base64.StdEncoding.EncodeToString([]byte(resultScript)), nil
}

func logCallerIdentity(ctx context.Context, cfg aws.Config, logs log.Logger) error {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}

	logs.Debugf("AWS Provider initialized - Account: %s, Region: %s, ARN: %s",
		aws.ToString(result.Account),
		cfg.Region,
		aws.ToString(result.Arn))
	return nil
}

// getCallerAccount returns the AWS account ID for logging context
func getCallerAccount(ctx context.Context, cfg aws.Config) string {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "unknown"
	}
	return aws.ToString(result.Account)
}
