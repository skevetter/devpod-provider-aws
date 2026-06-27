# AWS Provider for DevPod

[![Join us on Slack!](docs/static/media/slack.svg)](https://slack.loft.sh/) [![Open in DevPod!](https://devpod.sh/assets/open-in-devpod.svg)](https://devpod.sh/open#https://github.com/skevetter/devpod-provider-aws)

## Getting started

The provider is available for auto-installation using

```sh
devpod provider add aws
devpod provider use aws
```

Follow the on-screen instructions to complete the setup.

Needed variables will be:

- AWS_REGION or AWS_DEFAULT_REGION

The provider will inherit the login information from `aws cli` or you can
specify in your environment, or in the provider options, the `AWS_ACCESS_KEY_ID=`
and `AWS_SECRET_ACCESS_KEY=`

### Creating your first devpod env with aws

After the initial setup, just use:

```sh
devpod up .
```

You'll need to wait for the machine and environment setup.

### Customize the VM Instance

This provider has the following options.

#### Credentials

| NAME                          | REQUIRED | DESCRIPTION                                                                                                                                                                    | DEFAULT   |
| ----------------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------- |
| AWS_ACCESS_KEY_ID             | false    | The AWS access key id.                                                                                                                                                          |           |
| AWS_SECRET_ACCESS_KEY         | false    | The AWS secret access key.                                                                                                                                                      |           |
| AWS_SESSION_TOKEN             | false    | The AWS session token for temporary credentials.                                                                                                                               |           |
| AWS_PROFILE                   | false    | The AWS profile name to use.                                                                                                                                                    | `default` |
| CUSTOM_AWS_CREDENTIAL_COMMAND | false    | Shell command executed to get AWS credentials. Must return JSON with `AccessKeyID` (required), `SecretAccessKey` (required) and `SessionToken` (optional).                     |           |

#### Instance & networking

| NAME                     | REQUIRED | DESCRIPTION                                                                                                                                                  | DEFAULT                                                               |
| ------------------------ | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| AWS_REGION               | true     | The AWS cloud region to create the VM in (e.g. `us-west-1`).                                                                                                  |                                                                       |
| AWS_AMI                  | false    | The disk image to use.                                                                                                                                       | latest ubuntu in the region with proper architecture for the instance |
| AWS_DISK_SIZE            | false    | The root disk size to use (in GB).                                                                                                                           | 40                                                                    |
| AWS_ROOT_DEVICE          | false    | The root device of the disk image.                                                                                                                           | The `RootDeviceName` property of the AMI, or `/dev/sda1` if undefined |
| AWS_INSTANCE_TYPE        | false    | The machine type to use.                                                                                                                                     | c5.xlarge                                                             |
| AWS_VPC_ID               | false    | The vpc id to use.                                                                                                                                           |                                                                       |
| AWS_SUBNET_ID            | false    | The subnet id to use. Multiple can be specified comma-separated; by default the one with the most available IPs is chosen. Overridden by AWS_AVAILABILITY_ZONE. | created if not specified                                              |
| AWS_SECURITY_GROUP_ID    | false    | The security group id to use. Multiple can be specified comma-separated.                                                                                     | created if not specified                                              |
| AWS_AVAILABILITY_ZONE    | false    | The availability zone to choose a subnet out of the desired zone.                                                                                            |                                                                       |
| AWS_INSTANCE_TAGS        | false    | Additional tags for the instance in the form of `Name=XXX,Value=YYY Name=ZZZ,Value=WWW`.                                                                     |                                                                       |
| AWS_INSTANCE_PROFILE_ARN | false    | The ARN of the instance profile to use for the VM.                                                                                                           | created if not specified                                              |
| AWS_USE_NESTED_VIRTUALIZATION | false | If `true`, nested virtualization is enabled for the EC2 instance.                                                                                           | false                                                                 |

#### Spot instances

| NAME                   | REQUIRED | DESCRIPTION                                                                                                                              | DEFAULT      |
| ---------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------- | ------------ |
| AWS_USE_SPOT_INSTANCE  | false    | Prefer Spot instead of On-Demand instances.                                                                                             | false        |
| AWS_SPOT_INSTANCE_TYPE | false    | The spot instance request type. `persistent` keeps the request active after interruption, `one-time` is a single fulfillment attempt. Only used when AWS_USE_SPOT_INSTANCE is true. | `persistent` |

#### Connectivity

| NAME                                | REQUIRED | DESCRIPTION                                                                                                                                                                                | DEFAULT |
| ----------------------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| AWS_USE_INSTANCE_CONNECT_ENDPOINT   | false    | If `true`, connect to the EC2 instance via the default instance connect endpoint for the current subnet.                                                                                  | false   |
| AWS_INSTANCE_CONNECT_ENDPOINT_ID    | false    | Specify which instance connect endpoint to use. Only works with AWS_USE_INSTANCE_CONNECT_ENDPOINT enabled.                                                                                |         |
| AWS_USE_SESSION_MANAGER             | false    | If `true`, connect to the EC2 instance via AWS Session Manager.                                                                                                                           | false   |
| AWS_KMS_KEY_ARN_FOR_SESSION_MANAGER | false    | The KMS key ARN to use for AWS Session Manager.                                                                                                                                          |         |
| AWS_USE_ROUTE53                     | false    | If `true`, create a Route53 record for the machine's IP and use that hostname on connect. Zone is configured by AWS_ROUTE53_ZONE_NAME, or looked up by the tag `devpod=devpod`.          | false   |
| AWS_ROUTE53_ZONE_NAME               | false    | The zone name of a Route53 hosted zone to use for the machine's DNS name.                                                                                                                |         |

#### Secondary data volume

| NAME                        | REQUIRED | DESCRIPTION                                                                                                                                          | DEFAULT     |
| --------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| AWS_DATA_VOLUME_SNAPSHOT_ID | false    | EBS snapshot ID to restore as a secondary data volume. Useful for skipping dependency installs and lengthy setup.                                   |             |
| AWS_DATA_VOLUME_SIZE        | false    | Size in GB for the secondary data volume. Required for new blank volumes; defaults to the snapshot size when restoring from a snapshot.             |             |
| AWS_DATA_VOLUME_DEVICE      | false    | Device name for the secondary data volume.                                                                                                          | `/dev/xvdf` |
| AWS_DATA_VOLUME_MOUNT_PATH  | false    | Mount path for the secondary data volume inside the instance.                                                                                       | `/data`     |
| AWS_DATA_VOLUME_TYPE        | false    | EBS volume type for the secondary data volume (e.g. `gp3`, `gp2`, `st1`, `sc1`).                                                                    | `gp3`       |

#### User-data hooks

These run as `root` during instance initialization. The value can be inline shell commands or an S3 script reference (`s3://bucket/path.sh`). Failures are logged to `/var/log/devpod-hook-<name>.log` but do not halt instance setup.

| NAME                 | REQUIRED | DESCRIPTION                                                                                              | DEFAULT |
| -------------------- | -------- | ------------------------------------------------------------------------------------------------------- | ------- |
| AWS_HOOK_POST_SSH    | false    | Commands or S3 script to run after user creation and SSH key injection, before the data volume mount.   |         |
| AWS_HOOK_POST_VOLUME | false    | Commands or S3 script to run after the data volume mount, at the end of the user-data script.           |         |

#### Agent & DevPod behavior

| NAME                      | REQUIRED | DESCRIPTION                                                          | DEFAULT                  |
| ------------------------- | -------- | ------------------------------------------------------------------- | ------------------------ |
| INACTIVITY_TIMEOUT        | false    | Automatically stop the VM after the inactivity period.              | 10m                      |
| INJECT_GIT_CREDENTIALS    | false    | Whether DevPod should inject git credentials into the remote host.  | true                     |
| INJECT_DOCKER_CREDENTIALS | false    | Whether DevPod should inject docker credentials into the remote host. | true                   |
| AGENT_PATH                | false    | The path where to inject the DevPod agent to.                       | /var/lib/toolbox/devpod  |

You will need a user profile able to:
  - Create/Start/Stop/Destroy instances
  - Create/Destroy security groups
  - Create/Destroy subnets
  - Create/Destroy instance profiles

Alternatively you'll need to provide the IDs/ARNs of the already created resources.
Instance Create/Start/Stop/Destroy permissions are mandatory for how the provider itself works.

Options can either be set in `env` or on the command line, for example:

```sh
devpod provider set-options -o AWS_AMI=my-custom-ami
```

You can use a variety of AWS_INSTANCE_TYPE, from [this list](https://github.com/skevetter/devpod-provider-aws/blob/ca830cc2b0f530436475ba29791391f80458ab6a/hack/provider/provider.yaml#L88), they include
AMD, Intel and ARM64 instances, the list is automatically suggested when using
the GUI application.

## Local Development

To build and test the provider locally, use [task](https://taskfile.dev/) `task build:provider:dev`. The provider file is created in `./dist/provider.yaml`.
