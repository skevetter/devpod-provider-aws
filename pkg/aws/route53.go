package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"
)

// GetDevpodRoute53Zone retrieves the Route53 zone for the devpod if applicable. A zone name can either be specified
// in the provider configuration or be detected by looking for a Route53 zone with a tag "devpod" with value "devpod".
func GetDevpodRoute53Zone(ctx context.Context, provider *AwsProvider) (route53Zone, error) {
	r53client := route53.NewFromConfig(provider.AwsConfig)
	if provider.Config.Route53ZoneName != "" {
		return findRoute53ZoneByName(ctx, r53client, provider.Config.Route53ZoneName)
	}

	return detectRoute53ZoneByTag(ctx, r53client, provider)
}

func findRoute53ZoneByName(
	ctx context.Context,
	r53client *route53.Client,
	name string,
) (route53Zone, error) {
	listZonesOut, err := r53client.ListHostedZonesByName(
		ctx,
		&route53.ListHostedZonesByNameInput{DNSName: aws.String(name)},
	)
	if err != nil {
		return route53Zone{}, fmt.Errorf("find Route53 zone %s: %w", name, err)
	}

	zoneName := name
	if !strings.HasSuffix(zoneName, ".") {
		zoneName += "."
	}

	for _, zone := range listZonesOut.HostedZones {
		if *zone.Name == zoneName {
			return route53Zone{
				id:      *zone.Id,
				Name:    zoneName,
				private: zone.Config.PrivateZone,
			}, nil
		}
	}

	return route53Zone{}, fmt.Errorf("unable to find Route53 zone %s", name)
}

func detectRoute53ZoneByTag(
	ctx context.Context,
	r53client *route53.Client,
	provider *AwsProvider,
) (route53Zone, error) {
	truncated := true
	var marker *string

	for truncated {
		hostedZoneList, err := r53client.ListHostedZones(ctx, &route53.ListHostedZonesInput{
			MaxItems: aws.Int32(100),
			Marker:   marker,
		})
		if err != nil {
			return route53Zone{}, handleListZonesError(err, provider)
		}

		if zone, found := findTaggedZone(ctx, r53client, hostedZoneList.HostedZones); found {
			return zone, nil
		}

		truncated = hostedZoneList.IsTruncated
		marker = hostedZoneList.NextMarker
	}

	return route53Zone{}, nil
}

func handleListZonesError(err error, provider *AwsProvider) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDenied" {
		provider.Log.Debugf(
			"Access denied to list hosted zones, skipping Route53 zone detection: %v",
			err,
		)
		return nil
	}

	return fmt.Errorf("list hosted zones: %w", err)
}

func findTaggedZone(
	ctx context.Context,
	r53client *route53.Client,
	zones []r53types.HostedZone,
) (route53Zone, bool) {
	hostedZoneById := make(map[string]*r53types.HostedZone, len(zones))
	for _, hostedZone := range zones {
		hostedZoneById[strings.TrimPrefix(*hostedZone.Id, "/"+string(r53types.TagResourceTypeHostedzone)+"/")] = &hostedZone
	}

	resources, err := r53client.ListTagsForResources(ctx, &route53.ListTagsForResourcesInput{
		ResourceType: r53types.TagResourceTypeHostedzone,
		ResourceIds:  slices.Collect(maps.Keys(hostedZoneById)),
	})
	if err != nil {
		return route53Zone{}, false
	}

	for _, resourceTagSet := range resources.ResourceTagSets {
		for _, tag := range resourceTagSet.Tags {
			if *tag.Key == tagKeyDevpod && *tag.Value == tagKeyDevpod {
				hz := hostedZoneById[*resourceTagSet.ResourceId]
				return route53Zone{
					id:      *resourceTagSet.ResourceId,
					Name:    strings.TrimSuffix(*hz.Name, "."),
					private: hz.Config.PrivateZone,
				}, true
			}
		}
	}

	return route53Zone{}, false
}

// route53Record holds the parameters for a Route53 A record upsert.
type route53Record struct {
	zoneID   string
	hostname string
	ip       string
}

// UpsertDevpodRoute53Record creates or updates a Route53 A record for the devpod hostname in the specified zone.
func UpsertDevpodRoute53Record(
	ctx context.Context,
	provider *AwsProvider,
	record route53Record,
) error {
	r53client := route53.NewFromConfig(provider.AwsConfig)
	if _, err := r53client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(record.zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name:            aws.String(record.hostname),
						Type:            r53types.RRTypeA,
						ResourceRecords: []r53types.ResourceRecord{{Value: &record.ip}},
						TTL:             aws.Int64(300),
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf(
			"upsert A record %q in zone %q to value %q: %w",
			record.hostname,
			record.zoneID,
			record.ip,
			err,
		)
	}
	return nil
}

// DeleteDevpodRoute53Record deletes a Route53 A record for the devpod hostname in the specified zone.
func DeleteDevpodRoute53Record(
	ctx context.Context,
	provider *AwsProvider,
	zone route53Zone,
	machine Machine,
) error {
	ip := machine.PrivateIP
	if !zone.private {
		ip = machine.PublicIP
	}

	r53client := route53.NewFromConfig(provider.AwsConfig)
	if _, err := r53client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zone.id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionDelete,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: aws.String(machine.Hostname),
						Type: r53types.RRTypeA,
						ResourceRecords: []r53types.ResourceRecord{
							{
								Value: aws.String(ip),
							},
						},
						TTL: aws.Int64(300),
					},
				},
			},
		},
	}); err != nil {
		var recordNotFoundErr *r53types.InvalidChangeBatch
		if errors.As(err, &recordNotFoundErr) {
			provider.Log.Warnf(
				"A record %q in zone %q with value %q not found, skipping deletion: %v",
				machine.Hostname,
				zone.id,
				ip,
				err,
			)
			return nil
		}
		return fmt.Errorf(
			"delete A record %q in zone %q with value %q: %w",
			machine.Hostname,
			zone.id,
			ip,
			err,
		)
	}
	return nil
}
