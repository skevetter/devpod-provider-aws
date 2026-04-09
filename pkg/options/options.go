package options

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	devicePathRe = regexp.MustCompile(`^/dev/[a-zA-Z0-9/]+$`)
	mountPathRe  = regexp.MustCompile(`^/[a-zA-Z0-9/_.-]+$`)
)

var (
	AWS_AMI                             = "AWS_AMI"
	AWS_DISK_SIZE                       = "AWS_DISK_SIZE"
	AWS_ROOT_DEVICE                     = "AWS_ROOT_DEVICE"
	AWS_INSTANCE_TYPE                   = "AWS_INSTANCE_TYPE"
	AWS_REGION                          = "AWS_REGION"
	AWS_SECURITY_GROUP_ID               = "AWS_SECURITY_GROUP_ID"
	AWS_SUBNET_ID                       = "AWS_SUBNET_ID"
	AWS_VPC_ID                          = "AWS_VPC_ID"
	AWS_AVAILABILITY_ZONE               = "AWS_AVAILABILITY_ZONE"
	AWS_INSTANCE_TAGS                   = "AWS_INSTANCE_TAGS"
	AWS_INSTANCE_PROFILE_ARN            = "AWS_INSTANCE_PROFILE_ARN"
	AWS_USE_NESTED_VIRTUALIZATION       = "AWS_USE_NESTED_VIRTUALIZATION"
	AWS_USE_INSTANCE_CONNECT_ENDPOINT   = "AWS_USE_INSTANCE_CONNECT_ENDPOINT"
	AWS_INSTANCE_CONNECT_ENDPOINT_ID    = "AWS_INSTANCE_CONNECT_ENDPOINT_ID"
	AWS_USE_SPOT_INSTANCE               = "AWS_USE_SPOT_INSTANCE"
	AWS_SPOT_INSTANCE_TYPE              = "AWS_SPOT_INSTANCE_TYPE"
	AWS_USE_SESSION_MANAGER             = "AWS_USE_SESSION_MANAGER"
	AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER = "AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER"
	AWS_USE_ROUTE53                     = "AWS_USE_ROUTE53"
	AWS_ROUTE53_ZONE_NAME               = "AWS_ROUTE53_ZONE_NAME"
	AWS_ACCESS_KEY_ID                   = "AWS_ACCESS_KEY_ID"
	AWS_SECRET_ACCESS_KEY               = "AWS_SECRET_ACCESS_KEY"
	AWS_SESSION_TOKEN                   = "AWS_SESSION_TOKEN"
	CUSTOM_AWS_CREDENTIAL_COMMAND       = "CUSTOM_AWS_CREDENTIAL_COMMAND"

	// Data volume options (all optional).
	AWS_DATA_VOLUME_SNAPSHOT_ID = "AWS_DATA_VOLUME_SNAPSHOT_ID"
	AWS_DATA_VOLUME_SIZE        = "AWS_DATA_VOLUME_SIZE"
	AWS_DATA_VOLUME_DEVICE      = "AWS_DATA_VOLUME_DEVICE"
	AWS_DATA_VOLUME_MOUNT_PATH  = "AWS_DATA_VOLUME_MOUNT_PATH"
	AWS_DATA_VOLUME_TYPE        = "AWS_DATA_VOLUME_TYPE"
)

type Options struct {
	DiskImage                  string
	DiskSizeGB                 int
	RootDevice                 string
	MachineFolder              string
	MachineID                  string
	MachineType                string
	VpcID                      string
	SubnetIDs                  []string
	AvailabilityZone           string
	SecurityGroupID            string
	InstanceProfileArn         string
	InstanceTags               string
	Zone                       string
	UseNestedVirtualization    bool
	UseInstanceConnectEndpoint bool
	InstanceConnectEndpointID  string
	UseSpotInstance            bool
	SpotInstanceType           string
	UseSessionManager          bool
	KmsKeyARNForSessionManager string
	UseRoute53Hostnames        bool
	Route53ZoneName            string
	CustomCredentialCommand    string
	AccessKeyID                string
	SecretAccessKey            string
	SessionToken               string

	// Optional secondary data volume
	DataVolumeSnapshotID string
	DataVolumeSizeGB     int
	DataVolumeDevice     string
	DataVolumeMountPath  string
	DataVolumeType       string
	DataVolumeID         string // populated at runtime after CreateVolume
}

// HasDataVolume reports whether a secondary data volume is configured.
func (o *Options) HasDataVolume() bool {
	return o.DataVolumeSnapshotID != "" || o.DataVolumeSizeGB > 0
}

var strTrue = "true"

func FromEnv(init, withFolder bool) (*Options, error) {
	retOptions := &Options{}

	var err error
	retOptions.CustomCredentialCommand = os.Getenv(CUSTOM_AWS_CREDENTIAL_COMMAND)

	retOptions.MachineType, err = fromEnvOrError(AWS_INSTANCE_TYPE)
	if err != nil {
		return nil, err
	}

	diskSizeGB, err := fromEnvOrError(AWS_DISK_SIZE)
	if err != nil {
		return nil, err
	}

	retOptions.DiskSizeGB, err = strconv.Atoi(diskSizeGB)
	if err != nil {
		return nil, err
	}

	retOptions.DiskImage = os.Getenv(AWS_AMI)
	retOptions.RootDevice = os.Getenv(AWS_ROOT_DEVICE)
	retOptions.SecurityGroupID = os.Getenv(AWS_SECURITY_GROUP_ID)
	retOptions.VpcID = os.Getenv(AWS_VPC_ID)
	retOptions.AvailabilityZone = os.Getenv(AWS_AVAILABILITY_ZONE)
	retOptions.InstanceTags = os.Getenv(AWS_INSTANCE_TAGS)
	retOptions.InstanceProfileArn = os.Getenv(AWS_INSTANCE_PROFILE_ARN)
	retOptions.Zone = os.Getenv(AWS_REGION)
	retOptions.UseNestedVirtualization = os.Getenv(AWS_USE_NESTED_VIRTUALIZATION) == strTrue
	retOptions.UseInstanceConnectEndpoint = os.Getenv(AWS_USE_INSTANCE_CONNECT_ENDPOINT) == strTrue
	retOptions.InstanceConnectEndpointID = os.Getenv(AWS_INSTANCE_CONNECT_ENDPOINT_ID)
	retOptions.UseSpotInstance = os.Getenv(AWS_USE_SPOT_INSTANCE) == strTrue
	retOptions.SpotInstanceType = os.Getenv(AWS_SPOT_INSTANCE_TYPE)
	if retOptions.SpotInstanceType == "" {
		retOptions.SpotInstanceType = "persistent"
	}
	retOptions.UseSessionManager = os.Getenv(AWS_USE_SESSION_MANAGER) == strTrue
	retOptions.KmsKeyARNForSessionManager = os.Getenv(AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER)
	retOptions.UseRoute53Hostnames = os.Getenv(AWS_USE_ROUTE53) == strTrue
	retOptions.Route53ZoneName = os.Getenv(AWS_ROUTE53_ZONE_NAME)
	retOptions.AccessKeyID = os.Getenv(AWS_ACCESS_KEY_ID)
	retOptions.SecretAccessKey = os.Getenv(AWS_SECRET_ACCESS_KEY)
	retOptions.SessionToken = os.Getenv(AWS_SESSION_TOKEN)

	subnetIDs := os.Getenv(AWS_SUBNET_ID)
	if subnetIDs != "" {
		for _, subnetID := range strings.Split(subnetIDs, ",") {
			retOptions.SubnetIDs = append(retOptions.SubnetIDs, strings.TrimSpace(subnetID))
		}
	}

	// Optional data volume settings
	retOptions.DataVolumeSnapshotID = os.Getenv(AWS_DATA_VOLUME_SNAPSHOT_ID)
	retOptions.DataVolumeDevice = os.Getenv(AWS_DATA_VOLUME_DEVICE)
	if retOptions.DataVolumeDevice == "" {
		retOptions.DataVolumeDevice = "/dev/xvdf"
	}
	if !devicePathRe.MatchString(retOptions.DataVolumeDevice) {
		return nil, fmt.Errorf(
			"invalid %s: must be a valid device path like /dev/xvdf",
			AWS_DATA_VOLUME_DEVICE,
		)
	}
	retOptions.DataVolumeMountPath = os.Getenv(AWS_DATA_VOLUME_MOUNT_PATH)
	if retOptions.DataVolumeMountPath == "" {
		retOptions.DataVolumeMountPath = "/data"
	}
	if !mountPathRe.MatchString(retOptions.DataVolumeMountPath) {
		return nil, fmt.Errorf(
			"invalid %s: must be a valid absolute path like /data",
			AWS_DATA_VOLUME_MOUNT_PATH,
		)
	}
	retOptions.DataVolumeType = os.Getenv(AWS_DATA_VOLUME_TYPE)
	if retOptions.DataVolumeType == "" {
		retOptions.DataVolumeType = "gp3"
	}
	dataVolSize := os.Getenv(AWS_DATA_VOLUME_SIZE)
	if dataVolSize != "" {
		retOptions.DataVolumeSizeGB, err = strconv.Atoi(dataVolSize)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", AWS_DATA_VOLUME_SIZE, err)
		}
		if retOptions.DataVolumeSizeGB < 1 {
			return nil, fmt.Errorf("invalid %s: must be at least 1", AWS_DATA_VOLUME_SIZE)
		}
	}

	// Return early if we're just doing init
	if init {
		return retOptions, nil
	}

	retOptions.MachineID, err = fromEnvOrError("MACHINE_ID")
	if err != nil {
		return nil, err
	}
	// prefix with devpod-
	retOptions.MachineID = "devpod-" + retOptions.MachineID

	if withFolder {
		retOptions.MachineFolder, err = fromEnvOrError("MACHINE_FOLDER")
		if err != nil {
			return nil, err
		}
	}

	return retOptions, nil
}

func fromEnvOrError(name string) (string, error) {
	val := os.Getenv(name)
	if val == "" {
		return "", fmt.Errorf(
			"couldn't find option %s in environment, please make sure %s is defined",
			name,
			name,
		)
	}

	return val, nil
}
