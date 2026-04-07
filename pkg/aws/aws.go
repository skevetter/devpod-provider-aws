package aws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/sirupsen/logrus"
	"github.com/skevetter/devpod-provider-aws/pkg/options"
	"github.com/skevetter/devpod/pkg/client"
	"github.com/skevetter/devpod/pkg/ssh"
	"github.com/skevetter/log"
)

const (
	tagKeyDevpod               = "devpod"
	tagKeyHostname             = "devpod:hostname"
	devpodIAMResourceName      = "devpod-ec2-role"
	iamEC2PolicyName           = "devpod-ec2-policy"
	iamSSMKMSDecryptPolicyName = "ssm-kms-decrypt-policy"
)

// ErrInstanceNotFound is returned when no matching EC2 instance exists.
var ErrInstanceNotFound = errors.New("instance not found")

const defaultRootDevice = "/dev/sda1"

// detect if we're in an ec2 instance.
func isEC2Instance(ctx context.Context) bool {
	httpClient := &http.Client{Timeout: 1 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://instance-data.ec2.internal", nil)
	if err != nil {
		return false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return true
}

func configureDefaults(
	ctx context.Context,
	cfg aws.Config,
	config *options.Options,
	log log.Logger,
) error {
	isEC2 := isEC2Instance(ctx)
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
		config.RootDevice = defaultRootDevice
	}

	return nil
}

func setDefaultAMI(
	ctx context.Context,
	cfg aws.Config,
	config *options.Options,
	log log.Logger,
) error {
	log.Debugf(
		"disk image not specified, fetching default AMI for instance type %s",
		config.MachineType,
	)
	image, err := GetDefaultAMI(ctx, cfg, config.MachineType)
	if err != nil {
		return err
	}
	log.Debugf("using default AMI %s", image)
	config.DiskImage = image
	return nil
}

func setRootDevice(ctx context.Context, cfg aws.Config, config *options.Options, log log.Logger) {
	log.Debugf("determining root device for AMI %s", config.DiskImage)
	device, err := GetAMIRootDevice(ctx, cfg, config.DiskImage)
	if err != nil {
		log.Debugf(
			"could not determine root device for AMI %s: %v, using default %s",
			config.DiskImage,
			err,
			defaultRootDevice,
		)
		config.RootDevice = defaultRootDevice
	} else {
		log.Debugf("using root device: %s", device)
		config.RootDevice = device
	}
}

func NewAWSConfig(
	ctx context.Context,
	log log.Logger,
	options *options.Options,
) (aws.Config, error) {
	log.Debugf("configuring AWS SDK for region %s", options.Zone)
	opts, err := buildConfigOptions(ctx, log, options)
	if err != nil {
		return aws.Config{}, err
	}
	cfg, err := awsConfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, err
	}
	log.Debugf("AWS SDK configured")
	return cfg, nil
}

func buildConfigOptions(
	ctx context.Context,
	log log.Logger,
	options *options.Options,
) ([]func(*awsConfig.LoadOptions) error, error) {
	var opts []func(*awsConfig.LoadOptions) error

	if options.Zone != "" {
		opts = append(opts, awsConfig.WithRegion(options.Zone))
	}

	switch {
	case options.AccessKeyID != "" && options.SecretAccessKey != "":
		log.Debugf("using provided AWS credentials")
		opts = append(opts, awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     options.AccessKeyID,
				SecretAccessKey: options.SecretAccessKey,
				SessionToken:    options.SessionToken,
			},
		}))
		opts = append(opts, awsConfig.WithSharedConfigFiles([]string{}))
		opts = append(opts, awsConfig.WithSharedCredentialsFiles([]string{}))
	case options.CustomCredentialCommand != "":
		creds, err := executeCredentialCommand(ctx, options.CustomCredentialCommand, log)
		if err != nil {
			return nil, fmt.Errorf("custom credential command: %w", err)
		}
		opts = append(
			opts,
			awsConfig.WithCredentialsProvider(credentials.StaticCredentialsProvider{Value: creds}),
		)
		opts = append(opts, awsConfig.WithSharedConfigFiles([]string{}))
		opts = append(opts, awsConfig.WithSharedCredentialsFiles([]string{}))
	default:
		profile := os.Getenv("AWS_PROFILE")
		if profile != "" {
			log.Debugf("using AWS profile %s", profile)
		} else {
			log.Debugf("using default AWS credential chain")
		}
	}

	return opts, nil
}

func executeCredentialCommand(
	ctx context.Context,
	command string,
	log log.Logger,
) (aws.Credentials, error) {
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
		return aws.Credentials{}, fmt.Errorf(
			"custom credential command output missing required fields",
		)
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

// subnetResult holds the resolved subnet ID and its VPC ID.
type subnetResult struct {
	subnetID string
	vpcID    string
}

func GetSubnet(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	provider.Log.Debugf("getting subnet: vpc=%s az=%s subnets=%v",
		provider.Config.VpcID, provider.Config.AvailabilityZone, provider.Config.SubnetIDs)

	if len(provider.Config.SubnetIDs) == 1 {
		return describeSubnetResult(ctx, provider, provider.Config.SubnetIDs[0])
	}

	if len(provider.Config.SubnetIDs) > 1 {
		return selectFromSpecifiedSubnets(ctx, provider)
	}

	return discoverSubnet(ctx, provider)
}

func selectFromSpecifiedSubnets(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	subnetIDs := provider.Config.SubnetIDs
	az := provider.Config.AvailabilityZone
	provider.Log.Debugf("selecting subnet from %d specified subnets", len(subnetIDs))

	svc := ec2.NewFromConfig(provider.AwsConfig)
	subnets, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: subnetIDs})
	if err != nil {
		return subnetResult{}, fmt.Errorf("list specified subnets %q: %w", subnetIDs, err)
	}
	if len(subnets.Subnets) == 0 {
		return subnetResult{}, fmt.Errorf("no subnets found with IDs %q", subnetIDs)
	}

	subnet := selectSubnetWithMostIPs(subnets.Subnets, az)
	if subnet == nil {
		if az == "" {
			return subnetResult{}, fmt.Errorf("no subnets found with IDs %q", subnetIDs)
		}
		return subnetResult{}, fmt.Errorf(
			"no subnets found with IDs %q in availability zone %q",
			subnetIDs,
			az,
		)
	}

	provider.Log.Debugf(
		"selected subnet %s with %d available IPs",
		*subnet.SubnetId,
		*subnet.AvailableIpAddressCount,
	)
	return subnetResultFrom(subnet), nil
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

func describeSubnetResult(
	ctx context.Context,
	provider *AwsProvider,
	subnetID string,
) (subnetResult, error) {
	svc := ec2.NewFromConfig(provider.AwsConfig)
	out, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil {
		return subnetResult{}, fmt.Errorf("describe subnet %s: %w", subnetID, err)
	}
	if len(out.Subnets) == 0 {
		return subnetResult{}, fmt.Errorf("subnet %s not found", subnetID)
	}
	provider.Log.Debugf("using configured subnet %s (vpc: %s)", subnetID, *out.Subnets[0].VpcId)
	return subnetResultFrom(&out.Subnets[0]), nil
}

func subnetResultFrom(s *types.Subnet) subnetResult {
	r := subnetResult{subnetID: *s.SubnetId}
	if s.VpcId != nil {
		r.vpcID = *s.VpcId
	}
	return r
}

func discoverSubnet(ctx context.Context, provider *AwsProvider) (subnetResult, error) {
	vpcID := provider.Config.VpcID
	az := provider.Config.AvailabilityZone
	provider.Log.Debugf("searching for suitable subnet")

	svc := ec2.NewFromConfig(provider.AwsConfig)
	subnets, err := listAllSubnets(ctx, svc, az)
	if err != nil {
		return subnetResult{}, err
	}

	if subnet := findTaggedDevPodSubnet(filterByVPC(subnets, vpcID)); subnet != nil {
		provider.Log.Debugf(
			"found tagged subnet %s with %d available IPs",
			*subnet.SubnetId,
			*subnet.AvailableIpAddressCount,
		)
		return subnetResultFrom(subnet), nil
	}

	if subnet := findVPCPublicSubnet(subnets, vpcID); subnet != nil {
		provider.Log.Debugf(
			"found VPC subnet %s with %d available IPs",
			*subnet.SubnetId,
			*subnet.AvailableIpAddressCount,
		)
		return subnetResultFrom(subnet), nil
	}

	if vpcID == "" {
		return subnetResult{}, fmt.Errorf(
			"could not find a suitable subnet. Please either specify a subnet ID or VPC ID, or tag the desired" +
				" subnets with devpod=devpod",
		)
	}

	return subnetResult{}, fmt.Errorf(
		"no suitable subnet found in VPC %q. Please specify a subnet ID or tag subnets with devpod=devpod",
		vpcID,
	)
}

func listAllSubnets(ctx context.Context, svc *ec2.Client, az string) ([]types.Subnet, error) {
	input := &ec2.DescribeSubnetsInput{}
	if az != "" {
		input.Filters = []types.Filter{
			{Name: aws.String("availability-zone"), Values: []string{az}},
		}
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

func filterByVPC(subnets []types.Subnet, vpcID string) []types.Subnet {
	if vpcID == "" {
		return subnets
	}
	var filtered []types.Subnet
	for _, s := range subnets {
		if s.VpcId != nil && *s.VpcId == vpcID {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func findTaggedDevPodSubnet(subnets []types.Subnet) *types.Subnet {
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		if subnets[i].AvailableIpAddressCount == nil {
			continue
		}
		if isDevpodTagged(subnets[i].Tags) && *subnets[i].AvailableIpAddressCount > maxIPCount {
			maxIPCount = *subnets[i].AvailableIpAddressCount
			selected = &subnets[i]
		}
	}
	return selected
}

func isDevpodTagged(tags []types.Tag) bool {
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil &&
			*tag.Key == tagKeyDevpod && *tag.Value == tagKeyDevpod {
			return true
		}
	}
	return false
}

func findVPCPublicSubnet(subnets []types.Subnet, vpcID string) *types.Subnet {
	if vpcID == "" {
		return nil
	}
	var maxIPCount int32 = -1
	var selected *types.Subnet
	for i := range subnets {
		s := &subnets[i]
		if isPublicSubnetInVPC(s, vpcID) && *s.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *s.AvailableIpAddressCount
			selected = s
		}
	}
	return selected
}

func isPublicSubnetInVPC(s *types.Subnet, vpcID string) bool {
	return s.VpcId != nil && s.MapPublicIpOnLaunch != nil &&
		s.AvailableIpAddressCount != nil &&
		*s.VpcId == vpcID && *s.MapPublicIpOnLaunch
}

func GetDevpodVPC(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.VpcID != "" {
		provider.Log.Debugf("using configured VPC %s", provider.Config.VpcID)
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
			provider.Log.Debugf("using default VPC %s", *vpc.VpcId)
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
		return defaultRootDevice, nil
	}

	return *result.Images[0].RootDeviceName, nil
}

func GetDevpodInstanceProfile(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.InstanceProfileArn != "" {
		provider.Log.Debugf(
			"using configured instance profile %s",
			provider.Config.InstanceProfileArn,
		)
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

	provider.Log.Debugf("using existing instance profile %s", *response.InstanceProfile.Arn)
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

func GetDevpodSecurityGroups(
	ctx context.Context,
	provider *AwsProvider,
	vpcID string,
) ([]string, error) {
	if provider.Config.SecurityGroupID != "" {
		sgs := strings.Split(provider.Config.SecurityGroupID, ",")
		provider.Log.Debugf("using configured security groups %v", sgs)
		return sgs, nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)
	input := &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					tagKeyDevpod,
				},
			},
		},
	}

	if vpcID != "" {
		input.Filters = append(input.Filters, types.Filter{
			Name:   aws.String("vpc-id"),
			Values: []string{vpcID},
		})
	}

	result, err := svc.DescribeSecurityGroups(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("describe security groups: %w", err)
	}

	if len(result.SecurityGroups) == 0 {
		sg, err := CreateDevpodSecurityGroup(ctx, provider, vpcID)
		if err != nil {
			return nil, err
		}

		provider.Log.Debugf("created new security group %s", sg)
		return []string{sg}, nil
	}

	sgs := []string{}
	for res := range result.SecurityGroups {
		sgs = append(sgs, *result.SecurityGroups[res].GroupId)
	}

	provider.Log.Debugf("using existing security groups %v", sgs)
	return sgs, nil
}

func CreateDevpodSecurityGroup(
	ctx context.Context,
	provider *AwsProvider,
	vpcID string,
) (string, error) {
	svc := ec2.NewFromConfig(provider.AwsConfig)

	if vpcID == "" {
		var err error
		vpcID, err = GetDevpodVPC(ctx, provider)
		if err != nil {
			return "", err
		}
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
						Key:   aws.String(tagKeyDevpod),
						Value: aws.String(tagKeyDevpod),
					},
				},
			},
		},
		VpcId: aws.String(vpcID),
	})
	if err != nil {
		return "", err
	}

	groupID := *result.GroupId

	// No need to open ssh port if use session manager.
	if provider.Config.UseSessionManager {
		return groupID, nil
	}

	if err := authorizeSSHIngress(ctx, svc, groupID); err != nil {
		if _, delErr := svc.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: aws.String(groupID),
		}); delErr != nil {
			provider.Log.Warnf("failed to clean up security group %s: %v", groupID, delErr)
		}
		return "", err
	}

	return groupID, nil
}

func authorizeSSHIngress(ctx context.Context, svc *ec2.Client, groupID string) error {
	_, err := svc.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
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
						Key:   aws.String(tagKeyDevpod),
						Value: aws.String("devpod-ingress"),
					},
				},
			},
		},
	})
	return err
}

func anyState() []string {
	return []string{
		"pending",
		"running",
		"shutting-down",
		"stopped",
		"stopping",
	}
}

func GetDevpodInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, anyState())
}

func GetDevpodStoppedInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, []string{"stopped"})
}

func GetDevpodRunningInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
) (Machine, error) {
	return GetMachine(ctx, cfg, name, []string{"running"})
}

func GetMachine(
	ctx context.Context,
	cfg aws.Config,
	name string,
	states []string,
) (Machine, error) {
	instance, err := GetInstance(ctx, cfg, name, states)
	if err != nil {
		return Machine{}, err
	}
	return NewMachineFromInstance(instance), nil
}

func GetInstance(
	ctx context.Context,
	cfg aws.Config,
	name string,
	states []string,
) (types.Instance, error) {
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
				Name:   aws.String("instance-state-name"),
				Values: states,
			},
		},
	}

	result, err := svc.DescribeInstances(ctx, input)
	if err != nil {
		return types.Instance{}, err
	}

	// Sort slice in order to have the newest result first
	sort.Slice(result.Reservations, func(i, j int) bool {
		return result.Reservations[i].Instances[0].LaunchTime.After(
			*result.Reservations[j].Instances[0].LaunchTime,
		)
	})

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return types.Instance{}, ErrInstanceNotFound
	}
	return result.Reservations[0].Instances[0], nil
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
		{Key: aws.String(tagKeyDevpod), Value: aws.String(machineID)},
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

	reg := regexp.MustCompile(
		`Name=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+),Value=([A-Za-z0-9!"#$%&'()*+\-./:;<>?@[\\\]^_{|}~]+)`,
	)
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
	providerAws.Log.Debugf("creating instance: machine=%s type=%s ami=%s disk=%dGB",
		providerAws.Config.MachineID,
		providerAws.Config.MachineType,
		providerAws.Config.DiskImage,
		providerAws.Config.DiskSizeGB,
	)

	instance, r53Zone, err := buildRunInstancesInput(ctx, providerAws)
	if err != nil {
		return Machine{}, err
	}

	svc := ec2.NewFromConfig(cfg)

	providerAws.Log.Debugf("launching EC2 instance")
	result, err := svc.RunInstances(ctx, instance)
	if err != nil {
		return Machine{}, err
	}
	providerAws.Log.Debugf("EC2 instance launched: %s", *result.Instances[0].InstanceId)

	machine := NewMachineFromInstance(result.Instances[0])

	if r53Zone.id != "" {
		resolvedIP, err := upsertRoute53ForInstance(
			ctx,
			providerAws,
			r53Zone,
			result.Instances[0],
		)
		if err != nil {
			terminateOnCleanup(providerAws, *result.Instances[0].InstanceId)
			return Machine{}, fmt.Errorf("create Route53 record: %w", err)
		}
		machine.PublicIP = resolvedIP
	}

	providerAws.Log.Debugf("instance %s created", machine.InstanceID)
	return machine, nil
}

func buildRunInstancesInput(
	ctx context.Context,
	providerAws *AwsProvider,
) (*ec2.RunInstancesInput, route53Zone, error) {
	subnet, err := GetSubnet(ctx, providerAws)
	if err != nil {
		return nil, route53Zone{}, fmt.Errorf("determine subnet ID: %w", err)
	}
	devpodSG, err := resolveSecurityGroups(ctx, providerAws, subnet.vpcID)
	if err != nil {
		return nil, route53Zone{}, err
	}
	userData, err := GetInjectKeypairScript(providerAws.Config)
	if err != nil {
		return nil, route53Zone{}, err
	}
	r53Zone, err := resolveRoute53Zone(ctx, providerAws)
	if err != nil {
		return nil, route53Zone{}, err
	}
	volSizeI32, err := validatedDiskSize(providerAws.Config.DiskSizeGB)
	if err != nil {
		return nil, route53Zone{}, err
	}
	cfg := providerAws.Config
	instance := &ec2.RunInstancesInput{
		ImageId:          aws.String(cfg.DiskImage),
		InstanceType:     types.InstanceType(cfg.MachineType),
		MinCount:         aws.Int32(1),
		MaxCount:         aws.Int32(1),
		SecurityGroupIds: devpodSG,
		SubnetId:         aws.String(subnet.subnetID),
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpEndpoint:            types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(1),
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: aws.String(cfg.RootDevice),
				Ebs: &types.EbsBlockDevice{
					VolumeSize: &volSizeI32,
				},
			},
		},
		TagSpecifications: GetInstanceTags(providerAws, r53Zone),
		UserData:          &userData,
	}

	applyNestedVirtualization(providerAws, instance)
	applySpotInstance(providerAws, instance)
	if err := applyDataVolume(providerAws, instance); err != nil {
		return nil, route53Zone{}, err
	}

	if err := applyInstanceProfile(ctx, providerAws, instance); err != nil {
		return nil, route53Zone{}, err
	}

	return instance, r53Zone, nil
}

func resolveSecurityGroups(
	ctx context.Context,
	p *AwsProvider,
	subnetVPC string,
) ([]string, error) {
	vpcID := subnetVPC
	if vpcID == "" {
		vpcID = p.Config.VpcID
	}
	return GetDevpodSecurityGroups(ctx, p, vpcID)
}

func resolveRoute53Zone(ctx context.Context, p *AwsProvider) (route53Zone, error) {
	if !p.Config.UseRoute53Hostnames {
		return route53Zone{}, nil
	}
	return GetDevpodRoute53Zone(ctx, p)
}

func validatedDiskSize(size int) (int32, error) {
	if size < 0 || size > math.MaxInt32 {
		return 0, fmt.Errorf("invalid disk size: %d", size)
	}
	return int32(size), nil //nolint:gosec // bounds checked above
}

func applyNestedVirtualization(providerAws *AwsProvider, instance *ec2.RunInstancesInput) {
	if !providerAws.Config.UseNestedVirtualization {
		return
	}
	providerAws.Log.Debugf("enabling nested virtualization")
	instance.CpuOptions = &types.CpuOptionsRequest{
		NestedVirtualization: types.NestedVirtualizationSpecificationEnabled,
	}
}

func applySpotInstance(providerAws *AwsProvider, instance *ec2.RunInstancesInput) {
	if !providerAws.Config.UseSpotInstance {
		return
	}
	providerAws.Log.Debugf("using spot instance (type: %s)", providerAws.Config.SpotInstanceType)
	spotOpts := &types.SpotMarketOptions{
		SpotInstanceType: types.SpotInstanceType(providerAws.Config.SpotInstanceType),
	}
	if providerAws.Config.SpotInstanceType == "persistent" {
		spotOpts.InstanceInterruptionBehavior = "stop"
	}
	instance.InstanceMarketOptions = &types.InstanceMarketOptionsRequest{
		MarketType:  "spot",
		SpotOptions: spotOpts,
	}
}

// applyDataVolume adds an optional secondary EBS volume to the instance.
// When a snapshot ID is provided, the volume is restored from that snapshot,
// enabling fast workspace recovery without re-running lengthy setup steps.
func applyDataVolume(
	providerAws *AwsProvider,
	instance *ec2.RunInstancesInput,
) error {
	cfg := providerAws.Config
	if !cfg.HasDataVolume() {
		return nil
	}

	mapping := types.BlockDeviceMapping{
		DeviceName: aws.String(cfg.DataVolumeDevice),
		Ebs: &types.EbsBlockDevice{
			DeleteOnTermination: aws.Bool(true),
			VolumeType:          types.VolumeType(cfg.DataVolumeType),
		},
	}

	if cfg.DataVolumeSnapshotID != "" {
		providerAws.Log.Debugf("attaching data volume from snapshot %s",
			cfg.DataVolumeSnapshotID)
		mapping.Ebs.SnapshotId = aws.String(cfg.DataVolumeSnapshotID)
	}

	if cfg.DataVolumeSizeGB > 0 {
		size, err := validatedDiskSize(cfg.DataVolumeSizeGB)
		if err != nil {
			return fmt.Errorf("invalid data volume size: %w", err)
		}
		mapping.Ebs.VolumeSize = &size
	}

	instance.BlockDeviceMappings = append(instance.BlockDeviceMappings, mapping)
	providerAws.Log.Debugf("data volume configured: device=%s mount=%s",
		cfg.DataVolumeDevice, cfg.DataVolumeMountPath)

	return nil
}

func applyInstanceProfile(
	ctx context.Context,
	providerAws *AwsProvider,
	instance *ec2.RunInstancesInput,
) error {
	providerAws.Log.Debugf("getting instance profile")
	profile, err := GetDevpodInstanceProfile(ctx, providerAws)
	if err != nil {
		return fmt.Errorf("get instance profile: %w", err)
	}
	providerAws.Log.Debugf("using instance profile: %s", profile)
	instance.IamInstanceProfile = &types.IamInstanceProfileSpecification{
		Arn: aws.String(profile),
	}
	return nil
}

func upsertRoute53ForInstance(
	ctx context.Context,
	providerAws *AwsProvider,
	zone route53Zone,
	inst types.Instance,
) (string, error) {
	hostname := providerAws.Config.MachineID + "." + zone.Name
	ip := *inst.PrivateIpAddress

	if !zone.private {
		svc := ec2.NewFromConfig(providerAws.AwsConfig)

		publicIP, err := resolvePublicIP(ctx, providerAws, svc, inst)
		if err != nil {
			return "", err
		}

		ip = publicIP
	}

	providerAws.Log.Debugf("creating Route53 record: %s -> %s", hostname, ip)

	if err := UpsertDevpodRoute53Record(ctx, providerAws, route53Record{
		zoneID:   zone.id,
		hostname: hostname,
		ip:       ip,
	}); err != nil {
		return "", err
	}

	return ip, nil
}

func resolvePublicIP(
	ctx context.Context,
	providerAws *AwsProvider,
	svc *ec2.Client,
	inst types.Instance,
) (string, error) {
	if inst.PublicIpAddress != nil {
		return *inst.PublicIpAddress, nil
	}

	instanceID := *inst.InstanceId
	providerAws.Log.Debugf("waiting for public IP on instance %s", instanceID)

	waiter := ec2.NewInstanceRunningWaiter(svc)

	descOut, err := waiter.WaitForOutput(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("wait for instance running: %w", err)
	}

	if descOut.Reservations[0].Instances[0].PublicIpAddress == nil {
		return "", fmt.Errorf("instance %s has no public IP for public Route53 zone", instanceID)
	}

	return *descOut.Reservations[0].Instances[0].PublicIpAddress, nil
}

func Start(ctx context.Context, provider *AwsProvider, instanceID string) error {
	provider.Log.Debugf("starting instance %s", instanceID)

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

	provider.Log.Debugf("instance %s started", instanceID)
	return nil
}

func Stop(ctx context.Context, provider *AwsProvider, instanceID string) error {
	provider.Log.Debugf("stopping instance %s", instanceID)

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

	provider.Log.Debugf("instance %s stopped", instanceID)
	return nil
}

func Status(ctx context.Context, provider *AwsProvider, name string) (client.Status, error) {
	provider.Log.Debugf("checking status for machine %s", name)

	result, err := GetDevpodInstance(ctx, provider.AwsConfig, name)
	if err != nil {
		if errors.Is(err, ErrInstanceNotFound) {
			return client.StatusNotFound, nil
		}
		return client.StatusNotFound, fmt.Errorf("get instance: %w", err)
	}

	status := result.Status
	var clientStatus client.Status
	switch status {
	case "running":
		clientStatus = client.StatusRunning
	case "stopped":
		clientStatus = client.StatusStopped
	case "terminated":
		provider.Log.Debugf("machine %s terminated", name)
		return client.StatusNotFound, nil
	default:
		clientStatus = client.StatusBusy
	}

	provider.Log.Debugf("machine %s status is %s", name, status)
	return clientStatus, nil
}

func Describe(ctx context.Context, provider *AwsProvider, name string) (string, error) {
	provider.Log.Debugf("describing machine %s", name)

	instance, err := GetInstance(ctx, provider.AwsConfig, name, anyState())
	if err != nil {
		if errors.Is(err, ErrInstanceNotFound) {
			return client.DescriptionNotFound, nil
		}
		return "", fmt.Errorf("describe instance: %w", err)
	}

	instanceBytes, err := json.MarshalIndent(instance, "", "  ") // #nosec G117
	if err != nil {
		return "", fmt.Errorf("marshal instance description: %w", err)
	}

	description := string(instanceBytes)

	provider.Log.Debugf("machine %s is %s", name, description)
	return description, nil
}

func terminateOnCleanup(provider *AwsProvider, instanceID string) {
	provider.Log.Debugf("terminating orphaned instance %s", instanceID)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	svc := ec2.NewFromConfig(provider.AwsConfig)
	_, err := svc.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		provider.Log.Warnf("failed to terminate orphaned instance %s: %v", instanceID, err)
	}
}

func Delete(ctx context.Context, provider *AwsProvider, machine Machine) error {
	provider.Log.Debugf("deleting instance %s", machine.InstanceID)

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

	provider.Log.Debugf("instance %s terminated", machine.InstanceID)
	return nil
}

func GetInjectKeypairScript(config *options.Options) (string, error) {
	publicKeyBase, err := ssh.GetPublicKeyBase(config.MachineFolder)
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
if grep -q sudo /etc/group; then
	usermod -aG sudo devpod
elif grep -q wheel /etc/group; then
	usermod -aG wheel devpod
fi
echo "devpod ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/91-devpod
mkdir -p /home/devpod/.ssh
echo "` + string(publicKey) + `" >> /home/devpod/.ssh/authorized_keys
chmod 0700 /home/devpod/.ssh
chmod 0600 /home/devpod/.ssh/authorized_keys
chown -R devpod:devpod /home/devpod`

	resultScript += dataVolumeMountScript(config)

	return base64.StdEncoding.EncodeToString([]byte(resultScript)), nil
}

// dataVolumeMountScript returns a shell snippet that resolves the data volume
// device (including NVMe translation on Nitro instances), formats it if needed,
// and adds a persistent fstab entry.
// See: https://docs.aws.amazon.com/ebs/latest/userguide/nvme-ebs-volumes.html
// See: https://docs.aws.amazon.com/ebs/latest/userguide/identify-nvme-ebs-device.html
func dataVolumeMountScript(config *options.Options) string {
	if !config.HasDataVolume() {
		return ""
	}

	return fmt.Sprintf(`

# Mount secondary data volume. On Nitro instances, resolve NVMe device names.
DATA_DEV="%[1]s"
SNAPSHOT_ID="%[3]s"
if [ ! -b "$DATA_DEV" ]; then
  EXPECTED_SHORT=$(echo "%[1]s" | sed 's|^/dev/||')
  for nvmedev in /dev/nvme[0-9]*n1; do
    [ -b "$nvmedev" ] || continue
    MAPPED=""
    if command -v ebsnvme-id >/dev/null 2>&1; then
      MAPPED=$(ebsnvme-id -b "$nvmedev" 2>/dev/null)
    elif command -v nvme >/dev/null 2>&1; then
      MAPPED=$(nvme id-ctrl -V "$nvmedev" 2>/dev/null \
        | sed -n '/^vs\[\]/,$ { s/^.*"\(.*\)".*/\1/p }' \
        | tr -d ' .' | head -1)
    fi
    MAPPED_SHORT=$(echo "$MAPPED" | sed 's|^/dev/||')
    if [ "$MAPPED_SHORT" = "$EXPECTED_SHORT" ]; then
      DATA_DEV="$nvmedev"
      break
    fi
  done
fi
if [ ! -b "$DATA_DEV" ]; then
  echo "ERROR: data volume device %[1]s not found" >&2; exit 1
fi
mkdir -p "%[2]s"
if ! blkid "$DATA_DEV" >/dev/null 2>&1; then
  if [ -n "$SNAPSHOT_ID" ]; then
    echo "ERROR: snapshot volume $DATA_DEV has no recognizable filesystem" >&2
    exit 1
  fi
  mkfs.ext4 -q "$DATA_DEV"
fi
DATA_FSTYPE=$(blkid -s TYPE -o value "$DATA_DEV")
if [ -z "$DATA_FSTYPE" ]; then
  echo "ERROR: failed to detect filesystem type for $DATA_DEV" >&2
  exit 1
fi
DATA_UUID=$(blkid -s UUID -o value "$DATA_DEV")
if [ -z "$DATA_UUID" ]; then
  echo "ERROR: failed to get UUID for data volume $DATA_DEV" >&2
  exit 1
fi
if ! grep -q "UUID=$DATA_UUID" /etc/fstab; then
  echo "UUID=$DATA_UUID %[2]s $DATA_FSTYPE defaults,nofail 0 2" >> /etc/fstab
fi
mount -a
if ! mountpoint -q "%[2]s"; then
  echo "ERROR: failed to mount data volume at %[2]s" >&2; exit 1
fi
case "$DATA_FSTYPE" in ext4) resize2fs "$DATA_DEV" 2>/dev/null;; xfs) xfs_growfs "%[2]s" 2>/dev/null;; esac
chown devpod:devpod "%[2]s"`, config.DataVolumeDevice, config.DataVolumeMountPath, config.DataVolumeSnapshotID)
}

func logCallerIdentity(ctx context.Context, cfg aws.Config, logs log.Logger) error {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}

	logs.Debugf("AWS provider initialized - account: %s, region: %s, arn: %s",
		aws.ToString(result.Account),
		cfg.Region,
		aws.ToString(result.Arn))
	return nil
}

// getCallerAccount returns the AWS account ID for logging context.
func getCallerAccount(ctx context.Context, cfg aws.Config) string {
	svc := sts.NewFromConfig(cfg)
	result, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "unknown"
	}
	return aws.ToString(result.Account)
}
