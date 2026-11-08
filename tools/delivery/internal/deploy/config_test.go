package deploy

import "testing"

func TestDeploymentModeRequiresExplicitTestChoice(t *testing.T) {
	for _, test := range []struct {
		name     string
		config   Config
		accepted bool
	}{
		{"disabled test", Config{Mode: "test"}, false},
		{"disabled verified production", Config{Mode: "production", RecoveryVerified: true}, false},
		{"implicit production unverified", Config{Enabled: true}, false},
		{"implicit production verified", Config{Enabled: true, RecoveryVerified: true}, true},
		{"production unverified", Config{Enabled: true, Mode: "production"}, false},
		{"production verified", Config{Enabled: true, Mode: "production", RecoveryVerified: true}, true},
		{"explicit test", Config{Enabled: true, Mode: "test"}, true},
		{"unknown mode", Config{Enabled: true, Mode: "testing", RecoveryVerified: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.config.validateMode(); (err == nil) != test.accepted {
				t.Fatalf("accepted = %t, want %t, error = %v", err == nil, test.accepted, err)
			}
		})
	}
}
