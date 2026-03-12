package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// GetDevpodRoute53Zone retrieves the Route53 zone for the devpod if applicable. A zone name can either be specified
// in the provider configuration or be detected by looking for a Route53 zone with a tag "devpod" with value "devpod".
func GetDevpodRoute53Zone(ctx context.Context, provider *AwsProvider) (route53Zone, error) {
	r53client := route53.NewFromConfig(provider.AwsConfig)
	if provider.Config.Route53ZoneName != "" {
		return findRoute53ZoneByName(ctx, r53client, provider.Config.Route53ZoneName)
	}

	return detectRoute53ZoneByTag(ctx, r53client)
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

	var matches []route53Zone
	for _, zone := range listZonesOut.HostedZones {
		if *zone.Name == zoneName {
			matches = append(matches, route53Zone{
				id: strings.TrimPrefix(
					*zone.Id,
					"/"+string(r53types.TagResourceTypeHostedzone)+"/",
				),
				Name:    strings.TrimSuffix(zoneName, "."),
				private: zone.Config.PrivateZone,
			})
		}
	}

	switch len(matches) {
	case 0:
		return route53Zone{}, fmt.Errorf("unable to find Route53 zone %s", name)
	case 1:
		return matches[0], nil
	default:
		return route53Zone{}, fmt.Errorf(
			"found %d hosted zones matching %s, expected exactly one",
			len(matches),
			name,
		)
	}
}

func detectRoute53ZoneByTag(
	ctx context.Context,
	r53client *route53.Client,
) (route53Zone, error) {
	truncated := true
	var marker *string
	var allMatches []route53Zone

	for truncated {
		hostedZoneList, err := r53client.ListHostedZones(ctx, &route53.ListHostedZonesInput{
			MaxItems: aws.Int32(100),
			Marker:   marker,
		})
		if err != nil {
			return route53Zone{}, fmt.Errorf("list hosted zones: %w", err)
		}

		pageMatches, err := findTaggedZones(ctx, r53client, hostedZoneList.HostedZones)
		if err != nil {
			return route53Zone{}, err
		}
		allMatches = append(allMatches, pageMatches...)

		truncated = hostedZoneList.IsTruncated
		marker = hostedZoneList.NextMarker
	}

	switch len(allMatches) {
	case 0:
		return route53Zone{}, nil
	case 1:
		return allMatches[0], nil
	default:
		return route53Zone{}, fmt.Errorf(
			"found %d hosted zones tagged with devpod=devpod, expected exactly one",
			len(allMatches),
		)
	}
}

func findTaggedZones(
	ctx context.Context,
	r53client *route53.Client,
	zones []r53types.HostedZone,
) ([]route53Zone, error) {
	if len(zones) == 0 {
		return nil, nil
	}

	hostedZoneById := make(map[string]*r53types.HostedZone, len(zones))
	ids := make([]string, 0, len(zones))
	for _, hostedZone := range zones {
		id := strings.TrimPrefix(
			*hostedZone.Id,
			"/"+string(r53types.TagResourceTypeHostedzone)+"/",
		)
		hostedZoneById[id] = &hostedZone
		ids = append(ids, id)
	}

	tagSets, err := listTagsInBatches(ctx, r53client, ids)
	if err != nil {
		return nil, err
	}

	return collectDevpodZones(tagSets, hostedZoneById), nil
}

const maxTagResourceIDs = 10

func listTagsInBatches(
	ctx context.Context,
	r53client *route53.Client,
	ids []string,
) ([]r53types.ResourceTagSet, error) {
	var allTagSets []r53types.ResourceTagSet
	for i := 0; i < len(ids); i += maxTagResourceIDs {
		end := min(i+maxTagResourceIDs, len(ids))
		resp, err := r53client.ListTagsForResources(ctx, &route53.ListTagsForResourcesInput{
			ResourceType: r53types.TagResourceTypeHostedzone,
			ResourceIds:  ids[i:end],
		})
		if err != nil {
			return nil, fmt.Errorf("list tags for resources: %w", err)
		}
		allTagSets = append(allTagSets, resp.ResourceTagSets...)
	}
	return allTagSets, nil
}

func collectDevpodZones(
	tagSets []r53types.ResourceTagSet,
	zonesByID map[string]*r53types.HostedZone,
) []route53Zone {
	var matches []route53Zone
	for _, resourceTagSet := range tagSets {
		if !hasDevpodTag(resourceTagSet.Tags) {
			continue
		}
		hz := zonesByID[*resourceTagSet.ResourceId]
		matches = append(matches, route53Zone{
			id:      *resourceTagSet.ResourceId,
			Name:    strings.TrimSuffix(*hz.Name, "."),
			private: hz.Config.PrivateZone,
		})
	}
	return matches
}

func hasDevpodTag(tags []r53types.Tag) bool {
	for _, tag := range tags {
		if *tag.Key == tagKeyDevpod && *tag.Value == tagKeyDevpod {
			return true
		}
	}
	return false
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
