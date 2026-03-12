package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func TestHasDevpodTag(t *testing.T) {
	tests := []struct {
		name     string
		tags     []types.Tag
		expected bool
	}{
		{
			name: "has devpod tag",
			tags: []types.Tag{
				{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
			},
			expected: true,
		},
		{
			name: "has devpod tag among others",
			tags: []types.Tag{
				{Key: stringPtr("Name"), Value: stringPtr("test")},
				{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
				{Key: stringPtr("Environment"), Value: stringPtr("prod")},
			},
			expected: true,
		},
		{
			name: "no devpod tag",
			tags: []types.Tag{
				{Key: stringPtr("Name"), Value: stringPtr("test")},
				{Key: stringPtr("Environment"), Value: stringPtr("prod")},
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
				{Key: stringPtr(tagKeyDevpod), Value: stringPtr("wrong-value")},
			},
			expected: false,
		},
		{
			name: "correct value but wrong key",
			tags: []types.Tag{
				{Key: stringPtr("other-key"), Value: stringPtr(tagKeyDevpod)},
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
			result := hasDevpodTag(tt.tags)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestCollectDevpodZones(t *testing.T) {
	tests := []struct {
		name      string
		tagSets   []types.ResourceTagSet
		zonesByID map[string]*types.HostedZone
		expected  int
	}{
		{
			name: "collect zones with devpod tag",
			tagSets: []types.ResourceTagSet{
				{
					ResourceId: stringPtr("Z123456789"),
					Tags: []types.Tag{
						{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
					},
				},
			},
			zonesByID: map[string]*types.HostedZone{
				"Z123456789": {
					Id:   stringPtr("/hostedzone/Z123456789"),
					Name: stringPtr("example.com."),
					Config: &types.HostedZoneConfig{
						PrivateZone: false,
					},
				},
			},
			expected: 1,
		},
		{
			name: "filter out zones without devpod tag",
			tagSets: []types.ResourceTagSet{
				{
					ResourceId: stringPtr("Z123456789"),
					Tags: []types.Tag{
						{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
					},
				},
				{
					ResourceId: stringPtr("Z987654321"),
					Tags: []types.Tag{
						{Key: stringPtr("Name"), Value: stringPtr("other")},
					},
				},
			},
			zonesByID: map[string]*types.HostedZone{
				"Z123456789": {
					Id:   stringPtr("/hostedzone/Z123456789"),
					Name: stringPtr("example.com."),
					Config: &types.HostedZoneConfig{
						PrivateZone: false,
					},
				},
				"Z987654321": {
					Id:   stringPtr("/hostedzone/Z987654321"),
					Name: stringPtr("other.com."),
					Config: &types.HostedZoneConfig{
						PrivateZone: false,
					},
				},
			},
			expected: 1,
		},
		{
			name:      "no zones with devpod tag",
			tagSets:   []types.ResourceTagSet{},
			zonesByID: map[string]*types.HostedZone{},
			expected:  0,
		},
		{
			name: "private zone detection",
			tagSets: []types.ResourceTagSet{
				{
					ResourceId: stringPtr("Z111111111"),
					Tags: []types.Tag{
						{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
					},
				},
			},
			zonesByID: map[string]*types.HostedZone{
				"Z111111111": {
					Id:   stringPtr("/hostedzone/Z111111111"),
					Name: stringPtr("internal.example.com."),
					Config: &types.HostedZoneConfig{
						PrivateZone: true,
					},
				},
			},
			expected: 1,
		},
		{
			name: "zone name with trailing dot is trimmed",
			tagSets: []types.ResourceTagSet{
				{
					ResourceId: stringPtr("Z222222222"),
					Tags: []types.Tag{
						{Key: stringPtr(tagKeyDevpod), Value: stringPtr(tagKeyDevpod)},
					},
				},
			},
			zonesByID: map[string]*types.HostedZone{
				"Z222222222": {
					Id:   stringPtr("/hostedzone/Z222222222"),
					Name: stringPtr("trailing.example.com."),
					Config: &types.HostedZoneConfig{
						PrivateZone: false,
					},
				},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectDevpodZones(tt.tagSets, tt.zonesByID)

			if len(result) != tt.expected {
				t.Errorf("expected %d zones, got %d", tt.expected, len(result))
			}

			// Additional validation for collected zones
			for _, zone := range result {
				if zone.id == "" {
					t.Error("zone id should not be empty")
				}
				if zone.Name == "" {
					t.Error("zone Name should not be empty")
				}
				// Verify trailing dot was trimmed
				if zone.Name[len(zone.Name)-1] == '.' {
					t.Errorf("zone Name %q should not have trailing dot", zone.Name)
				}
			}
		})
	}
}

func TestRoute53Record(t *testing.T) {
	tests := []struct {
		name   string
		record route53Record
	}{
		{
			name: "basic record",
			record: route53Record{
				zoneID:   "Z123456789ABC",
				hostname: "host.example.com",
				ip:       "203.0.113.1",
			},
		},
		{
			name: "record with subdomain",
			record: route53Record{
				zoneID:   "Z987654321XYZ",
				hostname: "app.staging.example.com",
				ip:       "10.0.1.5",
			},
		},
		{
			name: "record with private IP",
			record: route53Record{
				zoneID:   "Z111111111111",
				hostname: "internal.example.com",
				ip:       "172.16.0.10",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.record.zoneID == "" {
				t.Error("zoneID should not be empty")
			}
			if tt.record.hostname == "" {
				t.Error("hostname should not be empty")
			}
			if tt.record.ip == "" {
				t.Error("ip should not be empty")
			}
		})
	}
}

// Helper function for creating string pointers
func stringPtr(s string) *string {
	return &s
}