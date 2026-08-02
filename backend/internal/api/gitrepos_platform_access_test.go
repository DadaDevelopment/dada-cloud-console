package api

import (
	"testing"

	"github.com/google/uuid"
)

func TestClassifyPlatformAccess(t *testing.T) {
	instID := uuid.New()

	cases := []struct {
		name           string
		provider       string
		installationID *uuid.UUID
		want           string
	}{
		{
			name:           "github with no installation is anonymous",
			provider:       "github",
			installationID: nil,
			want:           platformAccessAnonymous,
		},
		{
			name:           "github with a bound installation",
			provider:       "github",
			installationID: &instID,
			want:           platformAccessInstallation,
		},
		{
			name:           "gitlab holds a stored token so it is not anonymous",
			provider:       "gitlab",
			installationID: nil,
			want:           platformAccessInstallation,
		},
		{
			name:           "archive uploads have no provider credential to lose",
			provider:       "archive",
			installationID: nil,
			want:           platformAccessArchive,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPlatformAccess(tc.provider, tc.installationID)
			if got != tc.want {
				t.Errorf("classifyPlatformAccess(%q, %v) = %q, want %q", tc.provider, tc.installationID, got, tc.want)
			}
		})
	}
}
