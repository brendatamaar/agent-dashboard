package repo_test

import (
	"testing"

	"agent-dashboard/internal/core/repo"
)

func TestValidateRepoSettings(t *testing.T) {
	tests := []struct {
		name    string
		set     repo.RepoSettings
		wantErr bool
	}{
		{
			name:    "default valid",
			set:     repo.DefaultRepoSettings(),
			wantErr: false,
		},
		{
			name: "docker runtime with auto_approve allowed",
			set: repo.RepoSettings{
				Runtime:     "docker",
				AutoApprove: true,
			},
			wantErr: false,
		},
		{
			name: "host runtime valid when auto_approve is false",
			set: repo.RepoSettings{
				Runtime:     "host",
				AutoApprove: false,
			},
			wantErr: false,
		},
		{
			name: "host runtime with auto_approve forbidden",
			set: repo.RepoSettings{
				Runtime:     "host",
				AutoApprove: true,
			},
			wantErr: true,
		},
		{
			name: "invalid runtime name",
			set: repo.RepoSettings{
				Runtime: "kubernetes",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.ValidateRepoSettings(tt.set)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRepoSettings() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
